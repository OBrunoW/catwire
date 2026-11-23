package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

func newDelivery(t *testing.T, repository *postgres.WebhookDeliveryRepository, webhookID uuid.UUID) *domain.WebhookDelivery {
	t.Helper()
	delivery := &domain.WebhookDelivery{
		ID:        uuid.New(),
		WebhookID: webhookID,
		EventID:   uuid.New(),
		EventType: "message.received",
		Payload:   json.RawMessage(`{"ok":true}`),
	}
	inserted, err := repository.Insert(context.Background(), delivery)
	requireNoError(t, err)
	if !inserted {
		t.Fatal("expected delivery to be inserted")
	}
	return delivery
}

func deliveryStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var status string
	row := pool.QueryRow(context.Background(), `SELECT status FROM catwire.webhook_deliveries WHERE id = $1`, id)
	requireNoError(t, row.Scan(&status))
	return status
}

func TestWebhookFindByIDScopedToClient(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewWebhookRepository(pool)
	clientID := uniqueClientID()
	webhook := newWebhook(t, pool, clientID, []string{"message.received"}, nil)

	found, err := repository.FindByID(context.Background(), clientID, webhook.ID)
	requireNoError(t, err)
	if found.ID != webhook.ID || len(found.Events) != 1 || len(found.InstanceIDs) != 0 {
		t.Fatalf("unexpected webhook %+v", found)
	}

	_, err = repository.FindByID(context.Background(), uniqueClientID(), webhook.ID)
	requireAppError(t, err, domain.WebhookNotFound())
}

func TestWebhookListMatching(t *testing.T) {
	targetInstance := uuid.New()
	tests := []struct {
		name        string
		events      []string
		instanceIDs []uuid.UUID
		eventType   string
		inactive    bool
		expectMatch bool
	}{
		{name: "empty events and instances match everything", eventType: "message.received", expectMatch: true},
		{name: "event listed", events: []string{"message.received", "message.status"}, eventType: "message.status", expectMatch: true},
		{name: "event not listed", events: []string{"message.received"}, eventType: "message.status", expectMatch: false},
		{name: "instance listed", instanceIDs: []uuid.UUID{uuid.New(), targetInstance}, eventType: "message.received", expectMatch: true},
		{name: "instance not listed", instanceIDs: []uuid.UUID{uuid.New()}, eventType: "message.received", expectMatch: false},
		{name: "event and instance both match", events: []string{"message.received"}, instanceIDs: []uuid.UUID{targetInstance}, eventType: "message.received", expectMatch: true},
		{name: "event matches but instance does not", events: []string{"message.received"}, instanceIDs: []uuid.UUID{uuid.New()}, eventType: "message.received", expectMatch: false},
		{name: "inactive webhook is skipped", eventType: "message.received", inactive: true, expectMatch: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestPool(t)
			repository := postgres.NewWebhookRepository(pool)
			clientID := uniqueClientID()
			webhook := newWebhook(t, pool, clientID, test.events, test.instanceIDs)
			if test.inactive {
				inactive := false
				_, err := repository.Update(context.Background(), clientID, webhook.ID, domain.WebhookUpdate{Active: &inactive})
				requireNoError(t, err)
			}

			matching, err := repository.ListMatching(context.Background(), clientID, targetInstance, test.eventType)

			requireNoError(t, err)
			if test.expectMatch != (len(matching) == 1) {
				t.Fatalf("expected match=%v, got %d webhooks", test.expectMatch, len(matching))
			}
		})
	}
}

func TestWebhookListMatchingIgnoresOtherClientsAndDeleted(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewWebhookRepository(pool)
	clientID := uniqueClientID()
	newWebhook(t, pool, uniqueClientID(), nil, nil)
	deleted := newWebhook(t, pool, clientID, nil, nil)
	requireNoError(t, repository.SoftDelete(context.Background(), clientID, deleted.ID))

	matching, err := repository.ListMatching(context.Background(), clientID, uuid.New(), "message.received")

	requireNoError(t, err)
	if len(matching) != 0 {
		t.Fatalf("expected no webhooks, got %d", len(matching))
	}
}

func TestWebhookListByClientPaginates(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewWebhookRepository(pool)
	clientID := uniqueClientID()
	for range 3 {
		newWebhook(t, pool, clientID, nil, nil)
	}

	first, err := repository.ListByClient(context.Background(), clientID, nil, apihttp.PageParams{Limit: 2})
	requireNoError(t, err)
	if len(first.Data) != 2 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("unexpected first page %+v", first)
	}
	second, err := repository.ListByClient(context.Background(), clientID, nil, apihttp.PageParams{Limit: 2, Cursor: *first.NextCursor})

	requireNoError(t, err)
	if len(second.Data) != 1 || second.HasMore {
		t.Fatalf("unexpected second page %+v", second)
	}
	for _, item := range first.Data {
		if item.ID == second.Data[0].ID {
			t.Fatalf("webhook %s repeated across pages", item.ID)
		}
	}
}

func TestDeliveryInsertIsIdempotentPerWebhookAndEvent(t *testing.T) {
	pool := newTestPool(t)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	clientID := uniqueClientID()
	webhook := newWebhook(t, pool, clientID, nil, nil)
	original := newDelivery(t, deliveries, webhook.ID)
	duplicate := &domain.WebhookDelivery{ID: uuid.New(), WebhookID: webhook.ID, EventID: original.EventID, EventType: "message.received"}

	inserted, err := deliveries.Insert(context.Background(), duplicate)

	requireNoError(t, err)
	if inserted {
		t.Fatal("expected duplicate delivery to report inserted=false")
	}
	page, err := deliveries.ListByWebhook(context.Background(), clientID, webhook.ID, domain.DeliveryFilter{}, apihttp.PageParams{Limit: 10})
	requireNoError(t, err)
	if len(page.Data) != 1 || page.Data[0].ID != original.ID {
		t.Fatalf("expected the single original delivery, got %+v", page.Data)
	}
}

func TestDeliveryInsertSameEventForDifferentWebhooks(t *testing.T) {
	pool := newTestPool(t)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	clientID := uniqueClientID()
	first := newWebhook(t, pool, clientID, nil, nil)
	second := newWebhook(t, pool, clientID, nil, nil)
	original := newDelivery(t, deliveries, first.ID)
	sibling := &domain.WebhookDelivery{ID: uuid.New(), WebhookID: second.ID, EventID: original.EventID, EventType: "message.received"}

	inserted, err := deliveries.Insert(context.Background(), sibling)

	requireNoError(t, err)
	if !inserted {
		t.Fatal("expected the same event to be deliverable to another webhook")
	}
}

func TestDeliveryRecordFailureBecomesDeadAtMaxAttempts(t *testing.T) {
	tests := []struct {
		name             string
		maxAttempts      int
		failures         int
		expectedStatuses []string
	}{
		{name: "three attempts", maxAttempts: 3, failures: 3, expectedStatuses: []string{domain.DeliveryStatusFailed, domain.DeliveryStatusFailed, domain.DeliveryStatusDead}},
		{name: "single attempt", maxAttempts: 1, failures: 1, expectedStatuses: []string{domain.DeliveryStatusDead}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestPool(t)
			deliveries := postgres.NewWebhookDeliveryRepository(pool)
			webhook := newWebhook(t, pool, uniqueClientID(), nil, nil)
			delivery := newDelivery(t, deliveries, webhook.ID)
			statusCode := 500

			for attempt := range test.failures {
				status, err := deliveries.RecordFailure(context.Background(), delivery.ID, &statusCode, "server error", time.Now().Add(time.Minute), test.maxAttempts)

				requireNoError(t, err)
				if status != test.expectedStatuses[attempt] {
					t.Fatalf("attempt %d: expected %s, got %s", attempt+1, test.expectedStatuses[attempt], status)
				}
			}
			if stored := deliveryStatus(t, pool, delivery.ID); stored != test.expectedStatuses[test.failures-1] {
				t.Fatalf("expected stored status %s, got %s", test.expectedStatuses[test.failures-1], stored)
			}
		})
	}
}

func TestDeliveryRecordFailureUnknownDelivery(t *testing.T) {
	pool := newTestPool(t)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)

	_, err := deliveries.RecordFailure(context.Background(), uuid.New(), nil, "x", time.Now(), 3)

	requireAppError(t, err, domain.DeliveryNotFound())
}

func TestDeliveryRecordSuccess(t *testing.T) {
	pool := newTestPool(t)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	webhook := newWebhook(t, pool, uniqueClientID(), nil, nil)
	delivery := newDelivery(t, deliveries, webhook.ID)

	requireNoError(t, deliveries.RecordSuccess(context.Background(), delivery.ID, 200))

	if stored := deliveryStatus(t, pool, delivery.ID); stored != domain.DeliveryStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", stored)
	}
}

func TestWebhookSoftDeleteMarksOpenDeliveriesDead(t *testing.T) {
	pool := newTestPool(t)
	webhooks := postgres.NewWebhookRepository(pool)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	clientID := uniqueClientID()
	webhook := newWebhook(t, pool, clientID, nil, nil)
	pending := newDelivery(t, deliveries, webhook.ID)
	failed := newDelivery(t, deliveries, webhook.ID)
	_, err := deliveries.RecordFailure(context.Background(), failed.ID, nil, "err", time.Now(), 10)
	requireNoError(t, err)
	succeeded := newDelivery(t, deliveries, webhook.ID)
	requireNoError(t, deliveries.RecordSuccess(context.Background(), succeeded.ID, 200))

	requireNoError(t, webhooks.SoftDelete(context.Background(), clientID, webhook.ID))

	expectations := []struct {
		name     string
		id       uuid.UUID
		expected string
	}{
		{name: "pending", id: pending.ID, expected: domain.DeliveryStatusDead},
		{name: "failed", id: failed.ID, expected: domain.DeliveryStatusDead},
		{name: "succeeded", id: succeeded.ID, expected: domain.DeliveryStatusSucceeded},
	}
	for _, expectation := range expectations {
		if stored := deliveryStatus(t, pool, expectation.id); stored != expectation.expected {
			t.Fatalf("%s delivery: expected %s, got %s", expectation.name, expectation.expected, stored)
		}
	}
	_, err = webhooks.FindByID(context.Background(), clientID, webhook.ID)
	requireAppError(t, err, domain.WebhookNotFound())
}

func TestWebhookSoftDeleteOtherClientOrTwice(t *testing.T) {
	pool := newTestPool(t)
	webhooks := postgres.NewWebhookRepository(pool)
	clientID := uniqueClientID()
	webhook := newWebhook(t, pool, clientID, nil, nil)

	err := webhooks.SoftDelete(context.Background(), uniqueClientID(), webhook.ID)
	requireAppError(t, err, domain.WebhookNotFound())

	requireNoError(t, webhooks.SoftDelete(context.Background(), clientID, webhook.ID))
	err = webhooks.SoftDelete(context.Background(), clientID, webhook.ID)
	requireAppError(t, err, domain.WebhookNotFound())
}

func TestDeliveryClaimDueSkipsInactiveWebhooks(t *testing.T) {
	pool := newTestPool(t)
	webhooks := postgres.NewWebhookRepository(pool)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	clientID := uniqueClientID()
	active := newWebhook(t, pool, clientID, nil, nil)
	inactive := newWebhook(t, pool, clientID, nil, nil)
	activeDelivery := newDelivery(t, deliveries, active.ID)
	newDelivery(t, deliveries, inactive.ID)
	off := false
	_, err := webhooks.Update(context.Background(), clientID, inactive.ID, domain.WebhookUpdate{Active: &off})
	requireNoError(t, err)

	due, err := deliveries.ClaimDue(context.Background(), 100000, time.Minute)

	requireNoError(t, err)
	claimedForClient := 0
	for _, item := range due {
		if item.WebhookID == inactive.ID {
			t.Fatal("delivery of inactive webhook was claimed")
		}
		if item.ID == activeDelivery.ID {
			claimedForClient++
			if item.WebhookURL == "" || item.WebhookSecret == "" {
				t.Fatalf("expected webhook url and secret, got %+v", item)
			}
		}
	}
	if claimedForClient != 1 {
		t.Fatalf("expected active delivery claimed once, got %d", claimedForClient)
	}
}

func TestDeliveryStatusGuards(t *testing.T) {
	pool := newTestPool(t)
	webhooks := postgres.NewWebhookRepository(pool)
	deliveries := postgres.NewWebhookDeliveryRepository(pool)
	clientID := uniqueClientID()
	webhook := newWebhook(t, pool, clientID, nil, nil)
	pending := newDelivery(t, deliveries, webhook.ID)

	_, err := deliveries.ResetForRetry(context.Background(), clientID, webhook.ID, pending.ID)
	requireAppError(t, err, domain.DeliveryNotFound())

	requireNoError(t, webhooks.SoftDelete(context.Background(), clientID, webhook.ID))
	requireAppError(t, deliveries.RecordSuccess(context.Background(), pending.ID, 200), domain.DeliveryNotFound())
	_, err = deliveries.RecordFailure(context.Background(), pending.ID, nil, "x", time.Now(), 3)
	requireAppError(t, err, domain.DeliveryNotFound())
	if stored := deliveryStatus(t, pool, pending.ID); stored != domain.DeliveryStatusDead {
		t.Fatalf("expected dead kept, got %s", stored)
	}
}
