package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/domain"
)

const outboundRecipientPhone = "5511988887777"

type stubGateway struct {
	mu       sync.Mutex
	arrivals int
	sendErr  error
	sent     []outbound.SendCommand
}

func (g *stubGateway) GenerateMessageID(_ uuid.UUID) (string, error) {
	return "3EB0" + uuid.NewString(), nil
}

func (g *stubGateway) IsOnWhatsApp(_ context.Context, _ uuid.UUID, phone string) (outbound.Registration, error) {
	g.mu.Lock()
	g.arrivals++
	g.mu.Unlock()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		arrived := g.arrivals
		g.mu.Unlock()
		if arrived >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return outbound.Registration{Exists: true, JID: phone + "@s.whatsapp.net"}, nil
}

func (g *stubGateway) Send(_ context.Context, command outbound.SendCommand) (outbound.SendReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sendErr != nil {
		return outbound.SendReceipt{}, g.sendErr
	}
	g.sent = append(g.sent, command)
	return outbound.SendReceipt{Timestamp: time.Now().UTC()}, nil
}

type scopedMessageStore struct {
	outbound.MessageStore
	instanceID uuid.UUID
}

func (s scopedMessageStore) ListInstancesWithPendingSend(_ context.Context) ([]uuid.UUID, error) {
	return []uuid.UUID{s.instanceID}, nil
}

type failingOutbox struct{}

func (failingOutbox) Insert(_ context.Context, _ *domain.OutboxEvent) error {
	return errors.New("outbox unavailable")
}

type scopedOutboundUnitOfWork struct {
	inner         outbound.UnitOfWork
	instanceID    uuid.UUID
	failingOutbox bool
}

func (u scopedOutboundUnitOfWork) Do(ctx context.Context, run func(unit outbound.Unit) error) error {
	return u.inner.Do(ctx, func(unit outbound.Unit) error {
		unit.Messages = scopedMessageStore{MessageStore: unit.Messages, instanceID: u.instanceID}
		if u.failingOutbox {
			unit.Outbox = failingOutbox{}
		}
		return run(unit)
	})
}

type outboundFixture struct {
	pool     *pgxpool.Pool
	clientID string
	instance *domain.Instance
	gateway  *stubGateway
	unit     *postgres.OutboundUnitOfWork
}

func newConnectedInstance(t *testing.T, pool *pgxpool.Pool, clientID string) *domain.Instance {
	t.Helper()
	instance := newInstance(t, pool, clientID)
	_, err := postgres.NewInstanceRepository(pool).UpdateStatus(
		context.Background(), instance.ID, domain.InstanceStatusChange{Status: domain.InstanceStatusConnected},
	)
	requireNoError(t, err)
	instance.Status = domain.InstanceStatusConnected
	return instance
}

func newOutboundFixture(t *testing.T) *outboundFixture {
	t.Helper()
	pool := newTestPool(t)
	clientID := uniqueClientID()
	registerOutboxCleanup(t, pool, clientID)
	return &outboundFixture{
		pool:     pool,
		clientID: clientID,
		instance: newConnectedInstance(t, pool, clientID),
		gateway:  &stubGateway{},
		unit:     postgres.NewOutboundUnitOfWork(pool),
	}
}

func (f *outboundFixture) newService() *outbound.Service {
	return outbound.NewService(outbound.ServiceOptions{
		Unit:    f.unit,
		Gateway: f.gateway,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (f *outboundFixture) newWorker(failingOutbox bool) *outbound.Worker {
	return outbound.NewWorker(outbound.WorkerOptions{
		Unit:    scopedOutboundUnitOfWork{inner: f.unit, instanceID: f.instance.ID, failingOutbox: failingOutbox},
		Gateway: f.gateway,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (f *outboundFixture) countMessages(t *testing.T, instanceID uuid.UUID) int {
	t.Helper()
	var count int
	row := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM catwire.messages WHERE instance_id = $1`, instanceID)
	requireNoError(t, row.Scan(&count))
	return count
}

func (f *outboundFixture) outboxEventTypes(t *testing.T) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT event_type FROM catwire.outbox_events WHERE client_id = $1 ORDER BY seq`, f.clientID)
	requireNoError(t, err)
	defer rows.Close()
	types := []string{}
	for rows.Next() {
		var eventType string
		requireNoError(t, rows.Scan(&eventType))
		types = append(types, eventType)
	}
	requireNoError(t, rows.Err())
	return types
}

func textSendRequest(text string) outbound.SendRequest {
	return outbound.SendRequest{To: outboundRecipientPhone, Type: outbound.MessageTypeText, Text: &text}
}

func (f *outboundFixture) acceptPending(t *testing.T, text string) uuid.UUID {
	t.Helper()
	result, err := f.newService().Send(context.Background(), f.clientID, f.instance.ID, "", textSendRequest(text))
	requireNoError(t, err)
	return result.Message.ID
}

func TestListInstancesWithPendingSendReturnsOnlyInstancesWithPending(t *testing.T) {
	pool := newTestPool(t)
	withPending := newInstance(t, pool, uniqueClientID())
	withoutPending := newInstance(t, pool, uniqueClientID())
	withSentOnly := newInstance(t, pool, uniqueClientID())
	repository := postgres.NewMessageRepository(pool)
	pendingChat := newChat(t, pool, withPending.ID, uuid.NewString()+"@s.whatsapp.net")
	sentChat := newChat(t, pool, withSentOnly.ID, uuid.NewString()+"@s.whatsapp.net")
	insertMessage(t, pool, newMessage(withPending.ID, pendingChat.ID, domain.MessageStatusPending, microsecondsNow()))
	insertMessage(t, pool, newMessage(withSentOnly.ID, sentChat.ID, domain.MessageStatusSent, microsecondsNow()))

	ids, err := repository.ListInstancesWithPendingSend(context.Background())

	requireNoError(t, err)
	if !slices.Contains(ids, withPending.ID) {
		t.Fatalf("expected instance with pending message in %v", ids)
	}
	if slices.Contains(ids, withoutPending.ID) {
		t.Fatal("instance without messages must not be listed")
	}
	if slices.Contains(ids, withSentOnly.ID) {
		t.Fatal("instance with only sent messages must not be listed")
	}
}

func TestMessageInsertRepeatedIdempotencyKeyInSameInstanceIsRejected(t *testing.T) {
	pool := newTestPool(t)
	instance := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
	repository := postgres.NewMessageRepository(pool)
	key := "key-" + uuid.NewString()
	first := newMessage(instance.ID, chat.ID, domain.MessageStatusPending, microsecondsNow())
	first.IdempotencyKey = &key
	insertMessage(t, pool, first)
	second := newMessage(instance.ID, chat.ID, domain.MessageStatusPending, microsecondsNow())
	second.IdempotencyKey = &key

	_, err := repository.Insert(context.Background(), second)

	if !errors.Is(err, postgres.ErrIdempotencyKeyTaken) {
		t.Fatalf("expected ErrIdempotencyKeyTaken, got %v", err)
	}
	found, err := repository.FindByIdempotencyKey(context.Background(), instance.ID, key)
	requireNoError(t, err)
	if found.ID != first.ID {
		t.Fatalf("expected first message %s, got %s", first.ID, found.ID)
	}
	var count int
	row := pool.QueryRow(context.Background(), `SELECT count(*) FROM catwire.messages WHERE instance_id = $1`, instance.ID)
	requireNoError(t, row.Scan(&count))
	if count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
}

func TestMessageInsertSameIdempotencyKeyInAnotherInstanceIsAccepted(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMessageRepository(pool)
	key := "key-" + uuid.NewString()
	for range 2 {
		instance := newInstance(t, pool, uniqueClientID())
		chat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
		message := newMessage(instance.ID, chat.ID, domain.MessageStatusPending, microsecondsNow())
		message.IdempotencyKey = &key

		inserted, err := repository.Insert(context.Background(), message)

		requireNoError(t, err)
		if !inserted {
			t.Fatal("expected insert in another instance to succeed")
		}
	}
}

func TestServiceSendConcurrentSameKeyCreatesSingleMessage(t *testing.T) {
	fixture := newOutboundFixture(t)
	service := fixture.newService()
	request := textSendRequest("hello")
	key := "key-" + uuid.NewString()
	const callers = 2
	results := make([]outbound.SendResult, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results[index], errs[index] = service.Send(context.Background(), fixture.clientID, fixture.instance.ID, key, request)
		}()
	}
	close(start)
	group.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d returned error: %v", index, err)
		}
	}
	if count := fixture.countMessages(t, fixture.instance.ID); count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
	if results[0].Message.ID != results[1].Message.ID {
		t.Fatalf("expected both callers to see the same message, got %s and %s", results[0].Message.ID, results[1].Message.ID)
	}
	created := 0
	for _, result := range results {
		if !result.Replayed {
			created++
		}
	}
	if created > 1 {
		t.Fatalf("expected at most one non-replayed result, got %d", created)
	}
}

func TestServiceSendSameKeyWithDifferentBodyConflicts(t *testing.T) {
	fixture := newOutboundFixture(t)
	service := fixture.newService()
	key := "key-" + uuid.NewString()
	_, err := service.Send(context.Background(), fixture.clientID, fixture.instance.ID, key, textSendRequest("first"))
	requireNoError(t, err)

	_, err = service.Send(context.Background(), fixture.clientID, fixture.instance.ID, key, textSendRequest("second"))

	requireAppError(t, err, domain.IdempotencyConflict())
	if count := fixture.countMessages(t, fixture.instance.ID); count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
}

func TestServiceSendSameKeyWithSameBodyReplays(t *testing.T) {
	fixture := newOutboundFixture(t)
	service := fixture.newService()
	key := "key-" + uuid.NewString()
	first, err := service.Send(context.Background(), fixture.clientID, fixture.instance.ID, key, textSendRequest("hello"))
	requireNoError(t, err)

	second, err := service.Send(context.Background(), fixture.clientID, fixture.instance.ID, key, textSendRequest("hello"))

	requireNoError(t, err)
	if !second.Replayed || second.Message.ID != first.Message.ID {
		t.Fatalf("expected replay of %s, got %+v", first.Message.ID, second)
	}
	if first.Replayed {
		t.Fatal("expected first call not to be a replay")
	}
}

func TestWorkerSentStatusAndOutboxEventAreWrittenTogether(t *testing.T) {
	fixture := newOutboundFixture(t)
	messageID := fixture.acceptPending(t, "hello")

	requireNoError(t, fixture.newWorker(false).RunOnce(context.Background()))

	message, err := postgres.NewMessageRepository(fixture.pool).FindByID(context.Background(), fixture.instance.ID, messageID)
	requireNoError(t, err)
	if message.Status != domain.MessageStatusSent {
		t.Fatalf("expected sent, got %s", message.Status)
	}
	types := fixture.outboxEventTypes(t)
	if !slices.Equal(types, []string{ingestion.EventTypeMessageSent}) {
		t.Fatalf("expected one %s event, got %v", ingestion.EventTypeMessageSent, types)
	}
}

func TestWorkerSentStatusRollsBackWhenOutboxInsertFails(t *testing.T) {
	fixture := newOutboundFixture(t)
	messageID := fixture.acceptPending(t, "hello")

	requireNoError(t, fixture.newWorker(true).RunOnce(context.Background()))

	message, err := postgres.NewMessageRepository(fixture.pool).FindByID(context.Background(), fixture.instance.ID, messageID)
	requireNoError(t, err)
	if message.Status != domain.MessageStatusPending {
		t.Fatalf("expected status to roll back to pending, got %s", message.Status)
	}
	if count := countOutboxEvents(t, fixture.pool, fixture.clientID); count != 0 {
		t.Fatalf("expected no outbox events, got %d", count)
	}
}

func TestWorkerPermanentFailureMarksFailedAndEmitsEventTogether(t *testing.T) {
	fixture := newOutboundFixture(t)
	fixture.gateway.sendErr = &outbound.PermanentError{Code: "REJECTED", Message: "rejected by whatsapp"}
	messageID := fixture.acceptPending(t, "hello")

	requireNoError(t, fixture.newWorker(false).RunOnce(context.Background()))

	message, err := postgres.NewMessageRepository(fixture.pool).FindByID(context.Background(), fixture.instance.ID, messageID)
	requireNoError(t, err)
	if message.Status != domain.MessageStatusFailed {
		t.Fatalf("expected failed, got %s", message.Status)
	}
	if message.ErrorCode == nil || *message.ErrorCode != "REJECTED" {
		t.Fatalf("expected error code REJECTED, got %v", message.ErrorCode)
	}
	types := fixture.outboxEventTypes(t)
	if !slices.Equal(types, []string{outbound.EventTypeMessageFailed}) {
		t.Fatalf("expected one %s event, got %v", outbound.EventTypeMessageFailed, types)
	}
}

func TestWorkerPermanentFailureRollsBackWhenOutboxInsertFails(t *testing.T) {
	fixture := newOutboundFixture(t)
	fixture.gateway.sendErr = &outbound.PermanentError{Code: "REJECTED", Message: "rejected by whatsapp"}
	messageID := fixture.acceptPending(t, "hello")

	requireNoError(t, fixture.newWorker(true).RunOnce(context.Background()))

	message, err := postgres.NewMessageRepository(fixture.pool).FindByID(context.Background(), fixture.instance.ID, messageID)
	requireNoError(t, err)
	if message.Status != domain.MessageStatusPending {
		t.Fatalf("expected status to roll back to pending, got %s", message.Status)
	}
	if message.ErrorCode != nil {
		t.Fatalf("expected error code to roll back, got %v", *message.ErrorCode)
	}
}
