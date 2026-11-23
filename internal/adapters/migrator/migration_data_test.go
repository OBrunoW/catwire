package migrator_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/migrator"
	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
	"github.com/OBrunoW/catwire/migrations"
)

const ownedRowsFingerprint = `
SELECT
  (SELECT count(*) FROM catwire.instances WHERE client_id = $1),
  (SELECT count(*) FROM catwire.webhooks WHERE client_id = $1),
  (SELECT count(*) FROM catwire.outbox_events WHERE client_id = $1),
  (SELECT count(*) FROM catwire.schema_migrations WHERE version = 'V001'),
  md5(coalesce((SELECT string_agg(i::text, '|' ORDER BY i.id) FROM catwire.instances i WHERE i.client_id = $1), '')),
  md5(coalesce((SELECT string_agg(w::text, '|' ORDER BY w.id) FROM catwire.webhooks w WHERE w.client_id = $1), '')),
  md5(coalesce((SELECT string_agg(o::text, '|' ORDER BY o.seq) FROM catwire.outbox_events o WHERE o.client_id = $1), ''))
`

type rowsFingerprint struct {
	instances     int
	webhooks      int
	outboxEvents  int
	migrationRows int
	instancesHash string
	webhooksHash  string
	outboxHash    string
}

func openMigratedPool(t *testing.T) *pgxpool.Pool {
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
	if err := migrator.Apply(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

func seedOwnedRows(t *testing.T, pool *pgxpool.Pool, clientID string) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.outbox_events WHERE client_id = $1`, clientID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.webhooks WHERE client_id = $1`, clientID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE client_id = $1`, clientID)
	})
	instance := &domain.Instance{ID: uuid.New(), ClientID: clientID, Name: "seed", Status: domain.InstanceStatusCreated, AutoConnect: true}
	if err := postgres.NewInstanceRepository(pool).Insert(ctx, instance); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	hook := &domain.Webhook{ID: uuid.New(), ClientID: clientID, URL: "https://example.test/hook", Secret: "secret", Active: true}
	if err := postgres.NewWebhookRepository(pool).Insert(ctx, hook); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	event := &domain.OutboxEvent{
		EventID: uuid.New(), EventType: "whatsapp.message.received", EventVersion: 1, ClientID: clientID,
		InstanceID: &instance.ID, PartitionKey: clientID, Payload: json.RawMessage(`{"ok":true}`),
		OccurredAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if err := postgres.NewOutboxRepository(pool).Insert(ctx, event); err != nil {
		t.Fatalf("seed outbox event: %v", err)
	}
}

func readOwnedRows(t *testing.T, pool *pgxpool.Pool, clientID string) rowsFingerprint {
	t.Helper()
	var result rowsFingerprint
	err := pool.QueryRow(context.Background(), ownedRowsFingerprint, clientID).Scan(
		&result.instances, &result.webhooks, &result.outboxEvents, &result.migrationRows,
		&result.instancesHash, &result.webhooksHash, &result.outboxHash,
	)
	if err != nil {
		t.Fatalf("read fingerprint: %v", err)
	}
	return result
}

func applyRawScript(t *testing.T, pool *pgxpool.Pool, script []byte) {
	t.Helper()
	ctx := context.Background()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Conn().PgConn().Exec(ctx, string(script)).ReadAll(); err != nil {
		t.Fatalf("apply raw script: %v", err)
	}
}

func TestInitialMigrationAppliedTwiceKeepsSchemaAndData(t *testing.T) {
	pool := openMigratedPool(t)
	script, err := fs.ReadFile(migrations.Files, "V001__init.sql")
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	clientID := "migration-" + uuid.NewString()
	seedOwnedRows(t, pool, clientID)
	before := readOwnedRows(t, pool, clientID)

	applyRawScript(t, pool, script)
	applyRawScript(t, pool, script)

	after := readOwnedRows(t, pool, clientID)
	if before != after {
		t.Fatalf("data changed after reapplying V001: before=%+v after=%+v", before, after)
	}
	if before.instances != 1 || before.webhooks != 1 || before.outboxEvents != 1 || before.migrationRows != 1 {
		t.Fatalf("seed not visible in fingerprint: %+v", before)
	}
}
