package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/config"
)

const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("catwire stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           newRouter(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return serve(server)
}

func newRouter() http.Handler {
	router := chi.NewRouter()
	router.Get("/health", okHandler)
	router.Get("/ready", okHandler)
	return router
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	apihttp.WritePlain(w, http.StatusOK, map[string]string{"status": "ok"})
}

func serve(server *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	failures := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", server.Addr)
		failures <- server.ListenAndServe()
	}()

	select {
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
