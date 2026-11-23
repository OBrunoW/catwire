package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/application/outbox"
	"github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/domain"
)

const relayBatchSize = 1000

var errPublisherDown = errors.New("publisher down")

type recordingPublisher struct {
	mu       sync.Mutex
	failWith error
	eventIDs []string
}

func (p *recordingPublisher) Publish(_ context.Context, message outbox.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failWith != nil {
		return p.failWith
	}
	for _, header := range message.Headers {
		if header.Key == "event_id" {
			p.eventIDs = append(p.eventIDs, header.Value)
		}
	}
	return nil
}

func (p *recordingPublisher) publishedEventIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.eventIDs...)
}

type clientScopedOutbox struct {
	outbox.EventStore
	clientID string
}

func (s clientScopedOutbox) FetchPending(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	events, err := s.EventStore.FetchPending(ctx, limit)
	if err != nil {
		return nil, err
	}
	scoped := []domain.OutboxEvent{}
	for _, event := range events {
		if event.ClientID == s.clientID {
			scoped = append(scoped, event)
		}
	}
	return scoped, nil
}

type failingDeliveryWriter struct {
	outbox.DeliveryWriter
	failAtCall int
	calls      int
}

func (w *failingDeliveryWriter) Insert(ctx context.Context, delivery *domain.WebhookDelivery) (bool, error) {
	w.calls++
	if w.calls == w.failAtCall {
		return false, errors.New("delivery insert failed")
	}
	return w.DeliveryWriter.Insert(ctx, delivery)
}

type scopedUnitOfWork struct {
	inner      outbox.UnitOfWork
	clientID   string
	failAtCall int
}

func (u scopedUnitOfWork) Do(ctx context.Context, run func(unit outbox.Unit) error) error {
	return u.inner.Do(ctx, func(unit outbox.Unit) error {
		unit.Outbox = clientScopedOutbox{EventStore: unit.Outbox, clientID: u.clientID}
		if u.failAtCall > 0 {
			unit.Deliveries = &failingDeliveryWriter{DeliveryWriter: unit.Deliveries, failAtCall: u.failAtCall}
		}
		return run(unit)
	})
}

type fixedStatusSender struct {
	mu         sync.Mutex
	statusCode int
	err        error
	deliveries []uuid.UUID
}

func (s *fixedStatusSender) Send(_ context.Context, request webhook.Request) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveries = append(s.deliveries, request.DeliveryID)
	return s.statusCode, s.err
}

func (s *fixedStatusSender) sentTo(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sent := range s.deliveries {
		if sent == id {
			return true
		}
	}
	return false
}

func cleanRelayData(t *testing.T, pool *pgxpool.Pool, clientID string) {
	t.Helper()
	registerOutboxCleanup(t, pool, clientID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM catwire.webhook_deliveries
			WHERE webhook_id IN (SELECT id FROM catwire.webhooks WHERE client_id = $1)`, clientID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.webhooks WHERE client_id = $1`, clientID)
	})
}

func newRelayFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	clientID string,
	publisher outbox.Publisher,
	failDeliveryAtCall int,
) *outbox.Relay {
	t.Helper()
	cleanRelayData(t, pool, clientID)
	unit := scopedUnitOfWork{inner: postgres.NewOutboxUnitOfWork(pool), clientID: clientID, failAtCall: failDeliveryAtCall}
	return outbox.NewRelay(outbox.RelayOptions{Unit: unit, Publisher: publisher, BatchSize: relayBatchSize})
}

func insertRelayEvent(t *testing.T, pool *pgxpool.Pool, clientID, partitionKey string, instanceID *uuid.UUID) *domain.OutboxEvent {
	t.Helper()
	event := &domain.OutboxEvent{
		EventID:      uuid.New(),
		EventType:    "message.received",
		EventVersion: 1,
		ClientID:     clientID,
		InstanceID:   instanceID,
		PartitionKey: partitionKey,
		Payload:      json.RawMessage(`{"ok":true}`),
		OccurredAt:   microsecondsNow(),
	}
	requireNoError(t, postgres.NewOutboxRepository(pool).Insert(context.Background(), event))
	return event
}

func loadOutboxEvent(t *testing.T, pool *pgxpool.Pool, seq int64) domain.OutboxEvent {
	t.Helper()
	row := pool.QueryRow(context.Background(),
		`SELECT seq, event_id, event_type, event_version, client_id, instance_id, partition_key, payload,
			occurred_at, dispatched_at, kafka_published_at, attempts, next_attempt_at, last_error, created_at
		FROM catwire.outbox_events WHERE seq = $1`, seq)
	var event domain.OutboxEvent
	err := row.Scan(
		&event.Seq, &event.EventID, &event.EventType, &event.EventVersion, &event.ClientID, &event.InstanceID,
		&event.PartitionKey, &event.Payload, &event.OccurredAt, &event.DispatchedAt, &event.KafkaPublishedAt,
		&event.Attempts, &event.NextAttemptAt, &event.LastError, &event.CreatedAt,
	)
	requireNoError(t, err)
	return event
}

func countDeliveriesForEvent(t *testing.T, pool *pgxpool.Pool, eventID uuid.UUID) int {
	t.Helper()
	var count int
	row := pool.QueryRow(context.Background(), `SELECT count(*) FROM catwire.webhook_deliveries WHERE event_id = $1`, eventID)
	requireNoError(t, row.Scan(&count))
	return count
}

func countDeliveriesForWebhook(t *testing.T, pool *pgxpool.Pool, webhookID uuid.UUID, eventID uuid.UUID) int {
	t.Helper()
	var count int
	row := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.webhook_deliveries WHERE webhook_id = $1 AND event_id = $2`, webhookID, eventID)
	requireNoError(t, row.Scan(&count))
	return count
}

func deactivateWebhook(t *testing.T, pool *pgxpool.Pool, clientID string, id uuid.UUID) {
	t.Helper()
	inactive := false
	_, err := postgres.NewWebhookRepository(pool).Update(context.Background(), clientID, id, domain.WebhookUpdate{Active: &inactive})
	requireNoError(t, err)
}

func TestOutboxRelayPublishesAndFansOutToMatchingWebhooks(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &recordingPublisher{}
	relay := newRelayFixture(t, pool, clientID, publisher, 0)
	instance := newInstance(t, pool, clientID)
	active := newWebhook(t, pool, clientID, nil, nil)
	inactive := newWebhook(t, pool, clientID, nil, nil)
	deactivateWebhook(t, pool, clientID, inactive.ID)
	otherClient := newWebhook(t, pool, uniqueClientID(), nil, nil)
	event := insertRelayEvent(t, pool, clientID, clientID, &instance.ID)

	fetched, err := relay.RunOnce(context.Background())

	requireNoError(t, err)
	if fetched != 1 {
		t.Fatalf("expected 1 fetched event, got %d", fetched)
	}
	stored := loadOutboxEvent(t, pool, event.Seq)
	if stored.KafkaPublishedAt == nil || stored.DispatchedAt == nil {
		t.Fatalf("expected kafka_published_at and dispatched_at set, got %+v", stored)
	}
	if published := publisher.publishedEventIDs(); len(published) != 1 || published[0] != event.EventID.String() {
		t.Fatalf("expected event published once, got %v", published)
	}
	if countDeliveriesForWebhook(t, pool, active.ID, event.EventID) != 1 {
		t.Fatal("expected a delivery for the active matching webhook")
	}
	if countDeliveriesForWebhook(t, pool, inactive.ID, event.EventID) != 0 {
		t.Fatal("expected no delivery for the inactive webhook")
	}
	if countDeliveriesForWebhook(t, pool, otherClient.ID, event.EventID) != 0 {
		t.Fatal("expected no delivery for a webhook of another client")
	}
}

func TestOutboxRelayIgnoresAlreadyDispatchedEvents(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &recordingPublisher{}
	relay := newRelayFixture(t, pool, clientID, publisher, 0)
	newWebhook(t, pool, clientID, nil, nil)
	insertRelayEvent(t, pool, clientID, clientID, nil)
	_, err := relay.RunOnce(context.Background())
	requireNoError(t, err)

	fetched, err := relay.RunOnce(context.Background())

	requireNoError(t, err)
	if fetched != 0 || len(publisher.publishedEventIDs()) != 1 {
		t.Fatalf("expected no reprocessing, fetched %d published %v", fetched, publisher.publishedEventIDs())
	}
}

func TestOutboxRelayPublisherFailureSchedulesRetryWithoutDeliveries(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &recordingPublisher{failWith: errPublisherDown}
	relay := newRelayFixture(t, pool, clientID, publisher, 0)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	event := insertRelayEvent(t, pool, clientID, clientID, nil)

	fetched, err := relay.RunOnce(context.Background())

	requireNoError(t, err)
	if fetched != 1 {
		t.Fatalf("expected 1 fetched event, got %d", fetched)
	}
	stored := loadOutboxEvent(t, pool, event.Seq)
	if stored.KafkaPublishedAt != nil || stored.DispatchedAt != nil {
		t.Fatalf("expected event unpublished and undispatched, got %+v", stored)
	}
	if stored.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", stored.Attempts)
	}
	if !stored.NextAttemptAt.After(time.Now()) {
		t.Fatalf("expected next_attempt_at in the future, got %s", stored.NextAttemptAt)
	}
	if stored.LastError == nil {
		t.Fatal("expected last_error recorded")
	}
	if countDeliveriesForWebhook(t, pool, webhookRecord.ID, event.EventID) != 0 {
		t.Fatal("expected no delivery after publisher failure")
	}
}

func TestOutboxRelayKeepsPartitionOrderAfterFailure(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &recordingPublisher{failWith: errPublisherDown}
	relay := newRelayFixture(t, pool, clientID, publisher, 0)
	first := insertRelayEvent(t, pool, clientID, clientID, nil)
	second := insertRelayEvent(t, pool, clientID, clientID, nil)
	ctx := context.Background()

	_, err := relay.RunOnce(ctx)
	requireNoError(t, err)
	fetched, err := relay.RunOnce(ctx)
	requireNoError(t, err)

	if fetched != 0 {
		t.Fatalf("expected nothing fetched while head is delayed, got %d", fetched)
	}
	if len(publisher.publishedEventIDs()) != 0 {
		t.Fatalf("expected nothing published, got %v", publisher.publishedEventIDs())
	}
	if loadOutboxEvent(t, pool, second.Seq).Attempts != 0 {
		t.Fatal("expected second event untouched")
	}

	publisher.failWith = nil
	_, err = pool.Exec(ctx, `UPDATE catwire.outbox_events SET next_attempt_at = now() WHERE seq = $1`, first.Seq)
	requireNoError(t, err)
	_, err = relay.RunOnce(ctx)
	requireNoError(t, err)
	_, err = relay.RunOnce(ctx)
	requireNoError(t, err)

	published := publisher.publishedEventIDs()
	if len(published) != 2 || published[0] != first.EventID.String() || published[1] != second.EventID.String() {
		t.Fatalf("expected first then second published, got %v", published)
	}
}

func TestOutboxRelayRollsBackWholeBatchWhenDeliveryInsertFails(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &recordingPublisher{}
	relay := newRelayFixture(t, pool, clientID, publisher, 2)
	newWebhook(t, pool, clientID, nil, nil)
	first := insertRelayEvent(t, pool, clientID, "partition-a-"+clientID, nil)
	second := insertRelayEvent(t, pool, clientID, "partition-b-"+clientID, nil)

	_, err := relay.RunOnce(context.Background())

	if err == nil {
		t.Fatal("expected the batch to fail")
	}
	for _, event := range []*domain.OutboxEvent{first, second} {
		stored := loadOutboxEvent(t, pool, event.Seq)
		if stored.DispatchedAt != nil || stored.KafkaPublishedAt != nil {
			t.Fatalf("expected event %d untouched after rollback, got %+v", event.Seq, stored)
		}
		if countDeliveriesForEvent(t, pool, event.EventID) != 0 {
			t.Fatalf("expected no delivery for event %d after rollback", event.Seq)
		}
	}
}

func TestWebhookDeliveryInsertIsIdempotentPerWebhookAndEvent(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	eventID := uuid.New()
	build := func() *domain.WebhookDelivery {
		return &domain.WebhookDelivery{
			ID:        uuid.New(),
			WebhookID: webhookRecord.ID,
			EventID:   eventID,
			EventType: "message.received",
			Payload:   json.RawMessage(`{"ok":true}`),
		}
	}

	inserted, err := repository.Insert(context.Background(), build())
	requireNoError(t, err)
	repeated, err := repository.Insert(context.Background(), build())
	requireNoError(t, err)

	if !inserted || repeated {
		t.Fatalf("expected first insert true and repeat false, got %v and %v", inserted, repeated)
	}
	if countDeliveriesForWebhook(t, pool, webhookRecord.ID, eventID) != 1 {
		t.Fatal("expected a single delivery row")
	}
}

func TestWebhookDeliveryClaimDueThenRecordSuccess(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	delivery := newDelivery(t, repository, webhookRecord.ID)
	ctx := context.Background()

	claimed, err := repository.ClaimDue(ctx, relayBatchSize, time.Minute)
	requireNoError(t, err)

	found := findClaimed(claimed, delivery.ID)
	if found == nil {
		t.Fatal("expected the due delivery to be claimed")
	}
	if found.WebhookURL != webhookRecord.URL || found.WebhookSecret != webhookRecord.Secret {
		t.Fatalf("expected webhook url and secret on claim, got %+v", found)
	}
	again, err := repository.ClaimDue(ctx, relayBatchSize, time.Minute)
	requireNoError(t, err)
	if findClaimed(again, delivery.ID) != nil {
		t.Fatal("expected a leased delivery not to be claimed again")
	}

	requireNoError(t, repository.RecordSuccess(ctx, delivery.ID, 204))

	if status := deliveryStatus(t, pool, delivery.ID); status != domain.DeliveryStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", status)
	}
}

func TestWebhookDeliveryRecordFailureBecomesDeadAtMaxAttempts(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	delivery := newDelivery(t, repository, webhookRecord.ID)
	ctx := context.Background()
	nextAttempt := time.Now().Add(time.Minute)

	firstStatus, err := repository.RecordFailure(ctx, delivery.ID, nil, "boom", nextAttempt, 2)
	requireNoError(t, err)
	secondStatus, err := repository.RecordFailure(ctx, delivery.ID, nil, "boom", nextAttempt, 2)
	requireNoError(t, err)

	if firstStatus != domain.DeliveryStatusFailed || secondStatus != domain.DeliveryStatusDead {
		t.Fatalf("expected failed then dead, got %s then %s", firstStatus, secondStatus)
	}
	_, err = repository.RecordFailure(ctx, delivery.ID, nil, "boom", nextAttempt, 2)
	requireAppError(t, err, domain.DeliveryNotFound())
}

func TestWebhookDeliveryClaimDueSkipsInactiveWebhook(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	delivery := newDelivery(t, repository, webhookRecord.ID)
	deactivateWebhook(t, pool, clientID, webhookRecord.ID)

	claimed, err := repository.ClaimDue(context.Background(), relayBatchSize, time.Minute)

	requireNoError(t, err)
	if findClaimed(claimed, delivery.ID) != nil {
		t.Fatal("expected delivery of inactive webhook not to be claimed")
	}
}

func TestWebhookDispatcherMarksSucceededOnSuccessfulSend(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	delivery := newDelivery(t, repository, webhookRecord.ID)
	sender := &fixedStatusSender{statusCode: 200}
	dispatcher := webhook.NewDispatcher(webhook.DispatcherOptions{Store: repository, Sender: sender, BatchSize: relayBatchSize})

	_, err := dispatcher.RunOnce(context.Background())

	requireNoError(t, err)
	if !sender.sentTo(delivery.ID) {
		t.Fatal("expected the delivery to be sent")
	}
	if status := deliveryStatus(t, pool, delivery.ID); status != domain.DeliveryStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", status)
	}
}

func TestWebhookDispatcherMarksDeadWhenSendKeepsFailing(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	webhookRecord := newWebhook(t, pool, clientID, nil, nil)
	delivery := newDelivery(t, repository, webhookRecord.ID)
	sender := &fixedStatusSender{statusCode: 500}
	dispatcher := webhook.NewDispatcher(webhook.DispatcherOptions{
		Store: repository, Sender: sender, BatchSize: relayBatchSize, MaxAttempts: 1,
	})

	_, err := dispatcher.RunOnce(context.Background())

	requireNoError(t, err)
	if status := deliveryStatus(t, pool, delivery.ID); status != domain.DeliveryStatusDead {
		t.Fatalf("expected dead, got %s", status)
	}
}

func TestOutboxJanitorRemovesOnlyDispatchedEventsOlderThanRetention(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	cleanRelayData(t, pool, clientID)
	retention := time.Hour
	oldDispatched := insertRelayEvent(t, pool, clientID, "old-"+clientID, nil)
	recentDispatched := insertRelayEvent(t, pool, clientID, "recent-"+clientID, nil)
	pending := insertRelayEvent(t, pool, clientID, "pending-"+clientID, nil)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`UPDATE catwire.outbox_events SET dispatched_at = now() - interval '2 hours' WHERE seq = $1`, oldDispatched.Seq)
	requireNoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE catwire.outbox_events SET dispatched_at = now() - interval '10 minutes' WHERE seq = $1`, recentDispatched.Seq)
	requireNoError(t, err)
	janitor := outbox.NewJanitor(postgres.NewOutboxRepository(pool), retention, nil)

	removed, err := janitor.RunOnce(ctx)

	requireNoError(t, err)
	if removed < 1 {
		t.Fatalf("expected at least the old dispatched event removed, got %d", removed)
	}
	if remaining := countOutboxEvents(t, pool, clientID); remaining != 2 {
		t.Fatalf("expected recent and pending events to remain, got %d", remaining)
	}
	if !outboxEventExists(t, pool, recentDispatched.Seq) || !outboxEventExists(t, pool, pending.Seq) {
		t.Fatal("expected recent dispatched and pending events to remain")
	}
	if outboxEventExists(t, pool, oldDispatched.Seq) {
		t.Fatal("expected old dispatched event to be removed")
	}
}

func outboxEventExists(t *testing.T, pool *pgxpool.Pool, seq int64) bool {
	t.Helper()
	var exists bool
	row := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM catwire.outbox_events WHERE seq = $1)`, seq)
	requireNoError(t, row.Scan(&exists))
	return exists
}

func findClaimed(claimed []domain.DueDelivery, id uuid.UUID) *domain.DueDelivery {
	for index := range claimed {
		if claimed[index].ID == id {
			return &claimed[index]
		}
	}
	return nil
}
