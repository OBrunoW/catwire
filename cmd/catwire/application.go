package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/kafka"
	"github.com/OBrunoW/catwire/internal/adapters/migrator"
	"github.com/OBrunoW/catwire/internal/adapters/whatsapp"
	"github.com/OBrunoW/catwire/internal/application/outbox"
	"github.com/OBrunoW/catwire/internal/config"
	"github.com/OBrunoW/catwire/migrations"
)

const (
	shutdownTimeout   = 30 * time.Second
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	startupTimeout    = 30 * time.Second
	cleanupBatchSize  = 100
	cleanupInterval   = time.Hour
)

type application struct {
	cfg        config.Config
	logger     *slog.Logger
	pool       *pgxpool.Pool
	store      *whatsapp.Store
	publisher  *kafka.Publisher
	graph      *graph
	server     *http.Server
	workers    *taskGroup
	relay      *taskGroup
	dispatcher *taskGroup
}

func newApplication(ctx context.Context, cfg config.Config, logger *slog.Logger) (*application, error) {
	pool, err := openPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := migrator.Apply(ctx, pool, migrations.Files); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := whatsapp.OpenStore(ctx, cfg.DatabaseURL, whatsapp.NewLogger(logger))
	if err != nil {
		pool.Close()
		return nil, err
	}
	var publisher *kafka.Publisher
	if len(cfg.KafkaBrokers) > 0 {
		publisher = kafka.NewPublisher(cfg.KafkaBrokers)
	}
	built, err := assemble(cfg, pool, store, publisher, logger)
	if err != nil {
		closeResources(pool, store, publisher, logger)
		return nil, err
	}
	return &application{
		cfg:       cfg,
		logger:    logger,
		pool:      pool,
		store:     store,
		publisher: publisher,
		graph:     built,
		server: &http.Server{
			Addr:              ":" + cfg.Port,
			Handler:           built.handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		},
	}, nil
}

func openPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return pool, nil
}

func (a *application) run(ctx context.Context) error {
	a.startBackground(ctx)
	if err := a.graph.sessions.Start(ctx); err != nil {
		a.logger.Error("session manager failed to start", "error", err)
		return errors.Join(fmt.Errorf("session manager start: %w", err), a.shutdown())
	}
	failures := make(chan error, 1)
	go func() {
		a.logger.Info("http server listening", "addr", a.server.Addr)
		failures <- a.server.ListenAndServe()
	}()
	var serveErr error
	select {
	case err := <-failures:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	return errors.Join(serveErr, a.shutdown())
}

func (a *application) startBackground(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	a.workers = newTaskGroup(ctx)
	a.workers.Go(a.graph.mediaWorker.Run)
	a.workers.Go(a.graph.sendWorker.Run)
	a.workers.Go(func(ctx context.Context) { runEvery(ctx, cleanupInterval, a.cleanMedia) })
	a.workers.Go(func(ctx context.Context) { runEvery(ctx, outbox.JanitorInterval, a.pruneOutbox) })
	a.relay = newTaskGroup(ctx)
	a.relay.Go(a.graph.relay.Run)
	a.dispatcher = newTaskGroup(ctx)
	a.dispatcher.Go(a.graph.dispatcher.Run)
}

func (a *application) cleanMedia(ctx context.Context) {
	for ctx.Err() == nil {
		removed, err := a.graph.mediaCleaner.RunOnce(ctx, cleanupBatchSize)
		if err != nil {
			a.logger.Error("media cleanup failed", "error", err)
			return
		}
		if removed < cleanupBatchSize {
			return
		}
	}
}

func (a *application) pruneOutbox(ctx context.Context) {
	removed, err := a.graph.janitor.RunOnce(ctx)
	if err != nil {
		a.logger.Error("outbox janitor failed", "error", err)
		return
	}
	if removed > 0 {
		a.logger.Info("outbox pruned", "removed", removed)
	}
}

func (a *application) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	steps := []struct {
		name string
		stop func(ctx context.Context) error
	}{
		{"http server", a.server.Shutdown},
		{"workers", stopGroup(a.workers)},
		{"outbox relay", stopGroup(a.relay)},
		{"webhook dispatcher", stopGroup(a.dispatcher)},
		{"session manager", a.stopSessions},
	}
	var failures []error
	for _, step := range steps {
		if err := step.stop(ctx); err != nil {
			a.logger.Error("shutdown step failed", "step", step.name, "error", err)
			failures = append(failures, fmt.Errorf("stop %s: %w", step.name, err))
		}
	}
	closeResources(a.pool, a.store, a.publisher, a.logger)
	return errors.Join(failures...)
}

func stopGroup(group *taskGroup) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if group == nil {
			return nil
		}
		return group.Stop(ctx)
	}
}

func (a *application) stopSessions(ctx context.Context) error {
	finished := make(chan struct{})
	go func() {
		a.graph.sessions.Stop()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeResources(pool *pgxpool.Pool, store *whatsapp.Store, publisher *kafka.Publisher, logger *slog.Logger) {
	pool.Close()
	if err := store.Close(); err != nil {
		logger.Error("close whatsapp store", "error", err)
	}
	if publisher != nil {
		if err := publisher.Close(); err != nil {
			logger.Error("close kafka publisher", "error", err)
		}
	}
}
