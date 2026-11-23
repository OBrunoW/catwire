package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/http/handlers"
	"github.com/OBrunoW/catwire/internal/adapters/kafka"
	"github.com/OBrunoW/catwire/internal/adapters/mediafetch"
	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/adapters/storage"
	webhookadapter "github.com/OBrunoW/catwire/internal/adapters/webhook"
	"github.com/OBrunoW/catwire/internal/adapters/whatsapp"
	"github.com/OBrunoW/catwire/internal/application/contacts"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/instance"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/application/operations"
	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/application/outbox"
	"github.com/OBrunoW/catwire/internal/application/query"
	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/config"
	"github.com/OBrunoW/catwire/internal/middleware"
)

const messageBodyOverheadBytes = 1 << 20

type repositories struct {
	instances  *postgres.InstanceRepository
	settings   *postgres.InstanceSettingsRepository
	chats      *postgres.ChatRepository
	messages   *postgres.MessageRepository
	media      *postgres.MediaRepository
	outbox     *postgres.OutboxRepository
	webhooks   *postgres.WebhookRepository
	deliveries *postgres.WebhookDeliveryRepository
}

func newRepositories(pool *pgxpool.Pool) repositories {
	return repositories{
		instances:  postgres.NewInstanceRepository(pool),
		settings:   postgres.NewInstanceSettingsRepository(pool),
		chats:      postgres.NewChatRepository(pool),
		messages:   postgres.NewMessageRepository(pool),
		media:      postgres.NewMediaRepository(pool),
		outbox:     postgres.NewOutboxRepository(pool),
		webhooks:   postgres.NewWebhookRepository(pool),
		deliveries: postgres.NewWebhookDeliveryRepository(pool),
	}
}

type graph struct {
	handler      http.Handler
	sessions     *session.Manager
	mediaWorker  *media.Worker
	mediaCleaner *media.Cleaner
	sendWorker   *outbound.Worker
	relay        *outbox.Relay
	janitor      *outbox.Janitor
	dispatcher   *webhook.Dispatcher
}

type mediaStack struct {
	storage *storage.Local
	signer  *media.Signer
	urls    *media.URLBuilder
	worker  *media.Worker
	library *media.Library
	content *media.ContentService
}

func assemble(
	cfg config.Config,
	pool *pgxpool.Pool,
	store *whatsapp.Store,
	publisher *kafka.Publisher,
	logger *slog.Logger,
) (*graph, error) {
	repos := newRepositories(pool)
	registry := whatsapp.NewClientRegistry()
	gateway := whatsapp.NewMediaGateway(registry)
	stack, err := newMediaStack(cfg, pool, repos, gateway, logger)
	if err != nil {
		return nil, err
	}
	ingestor := ingestion.NewService(ingestion.Options{
		Unit:     postgres.NewIngestionUnitOfWork(pool),
		Settings: repos.settings,
		Logger:   logger,
	})
	factory := whatsapp.NewFactory(store, whatsapp.NewLogger(logger), ingestor).WithMedia(registry, stack.worker)
	sessions := session.NewManager(session.Options{
		Instances: repos.instances,
		Unit:      postgres.NewSessionUnitOfWork(pool),
		Clients:   factory,
		Logger:    logger,
	})
	outboundUnit := postgres.NewOutboundUnitOfWork(pool)
	outboundGateway := whatsapp.NewSender(registry)
	sender := outbound.NewService(outbound.ServiceOptions{
		Unit:          outboundUnit,
		Gateway:       outboundGateway,
		Fetcher:       mediafetch.New(mediafetch.Options{AllowPrivate: cfg.IsDevelopment()}),
		Storage:       stack.storage,
		Converter:     storage.NewFFmpegConverter(cfg.FFmpegPath),
		MaxMediaBytes: cfg.MediaMaxBytes,
		Logger:        logger,
	})
	built := &graph{
		sessions:     sessions,
		mediaWorker:  stack.worker,
		mediaCleaner: media.NewCleaner(postgres.NewMediaUnitOfWork(pool), stack.storage),
		sendWorker: outbound.NewWorker(outbound.WorkerOptions{
			Unit:    outboundUnit,
			Gateway: outboundGateway,
			Storage: stack.storage,
			MaxAge:  time.Duration(cfg.SendMaxAgeSeconds) * time.Second,
			Logger:  logger,
		}),
		relay:      newRelay(pool, publisher, logger),
		janitor:    outbox.NewJanitor(repos.outbox, time.Duration(cfg.OutboxRetentionDays)*24*time.Hour, nil),
		dispatcher: newDispatcher(cfg, repos, logger),
	}
	built.handler = newHandler(cfg, handlerParts{
		repos:     repos,
		registry:  registry,
		media:     stack,
		sessions:  sessions,
		sender:    sender,
		publisher: publisher,
		pool:      pool,
		logger:    logger,
	})
	return built, nil
}

func newMediaStack(
	cfg config.Config,
	pool *pgxpool.Pool,
	repos repositories,
	gateway *whatsapp.MediaGateway,
	logger *slog.Logger,
) (mediaStack, error) {
	local, err := storage.NewLocal(cfg.MediaDir)
	if err != nil {
		return mediaStack{}, err
	}
	var signer *media.Signer
	if cfg.MediaURLSecret != "" {
		signer, err = media.NewSigner(cfg.MediaURLSecret, nil)
		if err != nil {
			return mediaStack{}, err
		}
	}
	ttl := time.Duration(cfg.MediaURLTTLSeconds) * time.Second
	urls := media.NewURLBuilder(signer, cfg.PublicBaseURL, ttl)
	library, err := media.NewLibrary(media.LibraryOptions{
		Instances: repos.instances,
		Media:     repos.media,
		Storage:   local,
		URLs:      urls,
		MaxBytes:  cfg.MediaMaxBytes,
		Logger:    logger,
	})
	if err != nil {
		return mediaStack{}, fmt.Errorf("media library: %w", err)
	}
	worker := media.NewWorker(media.Options{
		Unit:          postgres.NewMediaUnitOfWork(pool),
		Storage:       local,
		Downloader:    gateway,
		Converter:     storage.NewFFmpegConverter(cfg.FFmpegPath),
		Retries:       gateway,
		Signer:        signer,
		PublicBaseURL: cfg.PublicBaseURL,
		URLTTL:        ttl,
		MaxBytes:      cfg.MediaMaxBytes,
		Logger:        logger,
	})
	return mediaStack{
		storage: local,
		signer:  signer,
		urls:    urls,
		worker:  worker,
		library: library,
		content: media.NewContentService(repos.media, local, signer),
	}, nil
}

func newRelay(pool *pgxpool.Pool, publisher *kafka.Publisher, logger *slog.Logger) *outbox.Relay {
	options := outbox.RelayOptions{Unit: postgres.NewOutboxUnitOfWork(pool), Logger: logger}
	if publisher != nil {
		options.Publisher = publisher
	}
	return outbox.NewRelay(options)
}

func newDispatcher(cfg config.Config, repos repositories, logger *slog.Logger) *webhook.Dispatcher {
	timeout := time.Duration(cfg.WebhookTimeoutSeconds) * time.Second
	return webhook.NewDispatcher(webhook.DispatcherOptions{
		Store:       repos.deliveries,
		Sender:      webhookadapter.NewSender(webhookadapter.Options{Timeout: timeout, AllowPrivate: cfg.IsDevelopment()}),
		MaxAttempts: cfg.WebhookMaxAttempts,
		Timeout:     timeout,
		Logger:      logger,
	})
}

type handlerParts struct {
	repos     repositories
	registry  *whatsapp.ClientRegistry
	media     mediaStack
	sessions  *session.Manager
	sender    *outbound.Service
	publisher *kafka.Publisher
	pool      *pgxpool.Pool
	logger    *slog.Logger
}

func newHandler(cfg config.Config, parts handlerParts) http.Handler {
	repos := parts.repos
	instances := instance.NewService(instance.Options{
		Unit:      postgres.NewInstanceUnitOfWork(parts.pool),
		Instances: repos.instances,
		Settings:  repos.settings,
	})
	queries := query.NewService(query.Options{
		Instances: repos.instances,
		Messages:  repos.messages,
		Chats:     repos.chats,
		Media:     repos.media,
		URLs:      parts.media.urls,
	})
	operationsGateway := whatsapp.NewOperations(parts.registry)
	return handlers.NewRouter(handlers.Dependencies{
		Auth:              middleware.Auth(cfg),
		Instances:         instances,
		Sessions:          parts.sessions,
		Settings:          instances,
		Contacts:          contacts.NewService(repos.instances, parts.sender),
		Sender:            parts.sender,
		MessageQueries:    queries,
		ChatQueries:       queries,
		Reads:             operations.NewReadService(repos.instances, repos.chats, repos.messages, operationsGateway, parts.logger),
		Presence:          operations.NewPresenceService(repos.instances, repos.chats, operationsGateway, parts.logger),
		History:           operations.NewHistoryService(repos.instances, repos.chats, repos.messages, operationsGateway, parts.logger),
		Profiles:          operations.NewProfileService(repos.instances, operationsGateway, parts.logger),
		Privacy:           operations.NewPrivacyService(repos.instances, operationsGateway, parts.logger),
		Library:           parts.media.library,
		Content:           parts.media.content,
		SignedMediaAccess: parts.media.signer != nil,
		Webhooks: webhook.NewService(webhook.ServiceOptions{
			Webhooks:         repos.webhooks,
			Deliveries:       repos.deliveries,
			Instances:        repos.instances,
			AllowInsecureURL: cfg.IsDevelopment(),
		}),
		Health:           healthChecks(parts.pool, parts.publisher),
		MaxMediaBytes:    cfg.MediaMaxBytes,
		MessageBodyLimit: cfg.MediaMaxBytes/3*4 + messageBodyOverheadBytes,
	})
}

func healthChecks(pool *pgxpool.Pool, publisher *kafka.Publisher) handlers.HealthChecks {
	checks := handlers.HealthChecks{Database: pool.Ping}
	if publisher != nil {
		checks.Kafka = func(ctx context.Context) error { return publisher.Ping(ctx) }
	}
	return checks
}
