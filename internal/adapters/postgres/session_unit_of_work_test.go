package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/domain"
)

func newStatusChangedEvent(instance *domain.Instance, eventID uuid.UUID) *domain.OutboxEvent {
	instanceID := instance.ID
	return &domain.OutboxEvent{
		EventID:      eventID,
		EventType:    session.EventTypeInstanceStatusChanged,
		EventVersion: 1,
		ClientID:     instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: instance.ID.String(),
		Payload:      []byte(`{"status":"connected","previous_status":"created"}`),
		OccurredAt:   microsecondsNow(),
	}
}

func registerOutboxCleanup(t *testing.T, pool *pgxpool.Pool, clientID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.outbox_events WHERE client_id = $1`, clientID)
	})
}

func countOutboxEvents(t *testing.T, pool *pgxpool.Pool, clientID string) int {
	t.Helper()
	var count int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.outbox_events WHERE client_id = $1`, clientID,
	).Scan(&count)
	requireNoError(t, err)
	return count
}

func readInstanceStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var status string
	err := pool.QueryRow(context.Background(), `SELECT status FROM catwire.instances WHERE id = $1`, id).Scan(&status)
	requireNoError(t, err)
	return status
}

func transitionToConnected(unit session.Unit, instance *domain.Instance, event *domain.OutboxEvent) error {
	ctx := context.Background()
	previous, err := unit.Instances.UpdateStatus(ctx, instance.ID, domain.InstanceStatusChange{Status: domain.InstanceStatusConnected})
	if err != nil {
		return err
	}
	if previous == domain.InstanceStatusConnected {
		return nil
	}
	return unit.Outbox.Insert(ctx, event)
}

func TestSessionUnitOfWorkStatusChangeWritesInstanceAndOutboxTogether(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	registerOutboxCleanup(t, pool, clientID)
	instance := newInstance(t, pool, clientID)
	unitOfWork := postgres.NewSessionUnitOfWork(pool)
	event := newStatusChangedEvent(instance, uuid.New())

	err := unitOfWork.Do(context.Background(), func(unit session.Unit) error {
		return transitionToConnected(unit, instance, event)
	})

	requireNoError(t, err)
	if status := readInstanceStatus(t, pool, instance.ID); status != domain.InstanceStatusConnected {
		t.Fatalf("expected status connected, got %s", status)
	}
	var eventType string
	var storedInstanceID uuid.UUID
	err = pool.QueryRow(context.Background(),
		`SELECT event_type, instance_id FROM catwire.outbox_events WHERE client_id = $1`, clientID,
	).Scan(&eventType, &storedInstanceID)
	requireNoError(t, err)
	if eventType != "whatsapp.instance.status_changed" {
		t.Fatalf("unexpected event type %s", eventType)
	}
	if storedInstanceID != instance.ID {
		t.Fatalf("expected instance %s, got %s", instance.ID, storedInstanceID)
	}
}

func TestSessionUnitOfWorkRollsBackStatusWhenOutboxInsertFails(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	registerOutboxCleanup(t, pool, clientID)
	instance := newInstance(t, pool, clientID)
	repeatedEventID := uuid.New()
	requireNoError(t, postgres.NewOutboxRepository(pool).Insert(context.Background(), newStatusChangedEvent(instance, repeatedEventID)))
	unitOfWork := postgres.NewSessionUnitOfWork(pool)
	conflicting := newStatusChangedEvent(instance, repeatedEventID)

	err := unitOfWork.Do(context.Background(), func(unit session.Unit) error {
		return transitionToConnected(unit, instance, conflicting)
	})

	if err == nil {
		t.Fatal("expected error from duplicate outbox event id")
	}
	if status := readInstanceStatus(t, pool, instance.ID); status != domain.InstanceStatusCreated {
		t.Fatalf("expected status rolled back to created, got %s", status)
	}
	if count := countOutboxEvents(t, pool, clientID); count != 1 {
		t.Fatalf("expected only the pre-existing outbox event, got %d", count)
	}
}

func TestSessionUnitOfWorkSameStatusDoesNotWriteOutbox(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	registerOutboxCleanup(t, pool, clientID)
	instance := newInstance(t, pool, clientID)
	unitOfWork := postgres.NewSessionUnitOfWork(pool)

	err := unitOfWork.Do(context.Background(), func(unit session.Unit) error {
		return transitionToConnected(unit, instance, newStatusChangedEvent(instance, uuid.New()))
	})
	requireNoError(t, err)
	err = unitOfWork.Do(context.Background(), func(unit session.Unit) error {
		return transitionToConnected(unit, instance, newStatusChangedEvent(instance, uuid.New()))
	})

	requireNoError(t, err)
	if count := countOutboxEvents(t, pool, clientID); count != 1 {
		t.Fatalf("expected a single outbox event, got %d", count)
	}
}
