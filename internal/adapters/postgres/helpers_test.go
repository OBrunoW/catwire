package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

const migrationPath = "../../../migrations/V001__init.sql"

var (
	migrationOnce sync.Once
	migrationErr  error
)

func applyMigration(ctx context.Context, pool *pgxpool.Pool) error {
	script, err := os.ReadFile(migrationPath)
	if err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	_, err = connection.Conn().PgConn().Exec(ctx, string(script)).ReadAll()
	return err
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	migrationOnce.Do(func() { migrationErr = applyMigration(ctx, pool) })
	if migrationErr != nil {
		t.Fatalf("apply migration: %v", migrationErr)
	}
	return pool
}

func uniqueClientID() string {
	return "test-" + uuid.NewString()
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireAppError(t *testing.T, err error, expected *domain.AppError) {
	t.Helper()
	var appErr *domain.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error %s, got %v", expected.Code, err)
	}
	if appErr.Code != expected.Code {
		t.Fatalf("expected code %s, got %s", expected.Code, appErr.Code)
	}
}

func microsecondsNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func stringPointer(value string) *string {
	return &value
}

func newInstance(t *testing.T, pool *pgxpool.Pool, clientID string) *domain.Instance {
	t.Helper()
	ctx := context.Background()
	instance := &domain.Instance{
		ID:          uuid.New(),
		ClientID:    clientID,
		Name:        "instance",
		Status:      domain.InstanceStatusCreated,
		AutoConnect: true,
	}
	requireNoError(t, postgres.NewInstanceRepository(pool).Insert(ctx, instance))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE id = $1`, instance.ID)
	})
	return instance
}

func newChat(t *testing.T, pool *pgxpool.Pool, instanceID uuid.UUID, jid string) *domain.Chat {
	t.Helper()
	chat := &domain.Chat{ID: uuid.New(), InstanceID: instanceID, JID: jid, Type: domain.ChatTypeDirect}
	requireNoError(t, postgres.NewChatRepository(pool).Upsert(context.Background(), chat))
	return chat
}

func newMessage(instanceID, chatID uuid.UUID, status string, timestamp time.Time) *domain.Message {
	return &domain.Message{
		ID:          uuid.New(),
		InstanceID:  instanceID,
		ChatID:      chatID,
		WAMessageID: "wa-" + uuid.NewString(),
		Direction:   domain.DirectionOutbound,
		Origin:      "api",
		Source:      "live",
		SenderJID:   "sender@s.whatsapp.net",
		Type:        "text",
		Text:        stringPointer("hello"),
		Status:      status,
		Timestamp:   timestamp,
	}
}

func insertMessage(t *testing.T, pool *pgxpool.Pool, message *domain.Message) *domain.Message {
	t.Helper()
	inserted, err := postgres.NewMessageRepository(pool).Insert(context.Background(), message)
	requireNoError(t, err)
	if !inserted {
		t.Fatalf("message %s was not inserted", message.WAMessageID)
	}
	return message
}

func newWebhook(t *testing.T, pool *pgxpool.Pool, clientID string, events []string, instanceIDs []uuid.UUID) *domain.Webhook {
	t.Helper()
	webhook := &domain.Webhook{
		ID:          uuid.New(),
		ClientID:    clientID,
		URL:         "https://example.test/hook",
		Secret:      "secret",
		Events:      events,
		InstanceIDs: instanceIDs,
		Active:      true,
	}
	requireNoError(t, postgres.NewWebhookRepository(pool).Insert(context.Background(), webhook))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.webhooks WHERE id = $1`, webhook.ID)
	})
	return webhook
}
