package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

const outboxFetchLimit = 100000

func insertOutboxEvents(t *testing.T, repository *postgres.OutboxRepository, clientID string, count int) []*domain.OutboxEvent {
	t.Helper()
	events := make([]*domain.OutboxEvent, 0, count)
	for range count {
		event := &domain.OutboxEvent{
			EventID:      uuid.New(),
			EventType:    "message.received",
			EventVersion: 1,
			ClientID:     clientID,
			PartitionKey: clientID,
			Payload:      json.RawMessage(`{"ok":true}`),
			OccurredAt:   microsecondsNow(),
		}
		requireNoError(t, repository.Insert(context.Background(), event))
		events = append(events, event)
	}
	return events
}

func pendingForClient(t *testing.T, pool *pgxpool.Pool, repository *postgres.OutboxRepository, clientID string) []domain.OutboxEvent {
	t.Helper()
	pending, err := repository.FetchPending(context.Background(), pool, outboxFetchLimit)
	requireNoError(t, err)
	matching := []domain.OutboxEvent{}
	for _, event := range pending {
		if event.ClientID == clientID {
			matching = append(matching, event)
		}
	}
	return matching
}

func cleanOutbox(t *testing.T, pool *pgxpool.Pool, clientID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.outbox_events WHERE client_id = $1`, clientID)
	})
}

func TestOutboxFetchPendingOrdersBySeq(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewOutboxRepository(pool)
	clientID := uniqueClientID()
	cleanOutbox(t, pool, clientID)
	inserted := insertOutboxEvents(t, repository, clientID, 3)

	pending := pendingForClient(t, pool, repository, clientID)

	if len(pending) != 1 {
		t.Fatalf("expected only the head of the partition, got %d events", len(pending))
	}
	if pending[0].EventID != inserted[0].EventID {
		t.Fatalf("expected head event %s, got %s", inserted[0].EventID, pending[0].EventID)
	}
}

func TestOutboxMarkDispatchedRemovesFromPending(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewOutboxRepository(pool)
	clientID := uniqueClientID()
	cleanOutbox(t, pool, clientID)
	inserted := insertOutboxEvents(t, repository, clientID, 3)

	requireNoError(t, repository.MarkDispatched(context.Background(), []int64{inserted[0].Seq, inserted[1].Seq}))

	pending := pendingForClient(t, pool, repository, clientID)
	if len(pending) != 1 || pending[0].EventID != inserted[2].EventID {
		t.Fatalf("expected only the third event pending, got %+v", pending)
	}
}

func TestOutboxRecordFailureDelaysEvent(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewOutboxRepository(pool)
	clientID := uniqueClientID()
	cleanOutbox(t, pool, clientID)
	inserted := insertOutboxEvents(t, repository, clientID, 1)

	err := repository.RecordFailure(context.Background(), inserted[0].Seq, "kafka down", time.Now().Add(time.Hour))

	requireNoError(t, err)
	if pending := pendingForClient(t, pool, repository, clientID); len(pending) != 0 {
		t.Fatalf("expected delayed event to be hidden, got %+v", pending)
	}
	var attempts int
	row := pool.QueryRow(context.Background(), `SELECT attempts FROM catwire.outbox_events WHERE seq = $1`, inserted[0].Seq)
	requireNoError(t, row.Scan(&attempts))
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestOutboxDeleteDispatchedBeforeKeepsPending(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewOutboxRepository(pool)
	clientID := uniqueClientID()
	cleanOutbox(t, pool, clientID)
	inserted := insertOutboxEvents(t, repository, clientID, 2)
	requireNoError(t, repository.MarkDispatched(context.Background(), []int64{inserted[0].Seq}))

	_, err := repository.DeleteDispatchedBefore(context.Background(), time.Now().Add(time.Minute))

	requireNoError(t, err)
	var remaining int
	row := pool.QueryRow(context.Background(), `SELECT count(*) FROM catwire.outbox_events WHERE client_id = $1`, clientID)
	requireNoError(t, row.Scan(&remaining))
	if remaining != 1 {
		t.Fatalf("expected only the pending event to remain, got %d", remaining)
	}
}

func TestOutboxFetchPendingHoldsLaterEventsOfSamePartitionBehindFailure(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewOutboxRepository(pool)
	clientID := uniqueClientID()
	cleanOutbox(t, pool, clientID)
	inserted := insertOutboxEvents(t, repository, clientID, 3)
	requireNoError(t, repository.RecordFailure(context.Background(), inserted[0].Seq, "kafka down", time.Now().Add(time.Hour)))

	if pending := pendingForClient(t, pool, repository, clientID); len(pending) != 0 {
		t.Fatalf("expected later events held behind the failed one, got %+v", pending)
	}
}
