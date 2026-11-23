package outbox

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type fakeDB struct {
	mu         sync.Mutex
	now        time.Time
	events     []*domain.OutboxEvent
	webhooks   []domain.Webhook
	deliveries []domain.WebhookDelivery
	instances  map[uuid.UUID]*domain.Instance
	published  []string
}

func newFakeDB() *fakeDB {
	return &fakeDB{now: fixedNow, instances: map[uuid.UUID]*domain.Instance{}}
}

func (d *fakeDB) Do(_ context.Context, run func(unit Unit) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return run(Unit{Outbox: &fakeOutbox{db: d}, Webhooks: &fakeWebhooks{db: d}, Deliveries: &fakeDeliveries{db: d}, Instances: &fakeInstances{db: d}})
}

func (d *fakeDB) addEvent(instanceID uuid.UUID, partitionKey string) *domain.OutboxEvent {
	event := &domain.OutboxEvent{
		Seq:           int64(len(d.events) + 1),
		EventID:       uuid.New(),
		EventType:     "whatsapp.message.received",
		EventVersion:  1,
		ClientID:      "client-a",
		InstanceID:    &instanceID,
		PartitionKey:  partitionKey,
		Payload:       []byte(`{"message":{"id":"m1"}}`),
		OccurredAt:    fixedNow,
		NextAttemptAt: fixedNow,
	}
	d.events = append(d.events, event)
	return event
}

func (d *fakeDB) addInstance() *domain.Instance {
	externalID := "crm-user-42"
	phone := "5511999999999"
	instance := &domain.Instance{ID: uuid.New(), ClientID: "client-a", ExternalID: &externalID, Phone: &phone}
	d.instances[instance.ID] = instance
	return instance
}

type fakeOutbox struct{ db *fakeDB }

func (o *fakeOutbox) FetchPending(_ context.Context, limit int) ([]domain.OutboxEvent, error) {
	pending := []domain.OutboxEvent{}
	for _, candidate := range o.db.events {
		if candidate.DispatchedAt != nil || candidate.NextAttemptAt.After(o.db.now) || o.blockedByEarlier(candidate) {
			continue
		}
		pending = append(pending, *candidate)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Seq < pending[j].Seq })
	if len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

func (o *fakeOutbox) blockedByEarlier(candidate *domain.OutboxEvent) bool {
	for _, earlier := range o.db.events {
		if earlier.PartitionKey == candidate.PartitionKey && earlier.DispatchedAt == nil && earlier.Seq < candidate.Seq {
			return true
		}
	}
	return false
}

func (o *fakeOutbox) find(seq int64) *domain.OutboxEvent {
	for _, event := range o.db.events {
		if event.Seq == seq {
			return event
		}
	}
	return nil
}

func (o *fakeOutbox) MarkKafkaPublished(_ context.Context, seqs []int64) error {
	for _, seq := range seqs {
		now := o.db.now
		o.find(seq).KafkaPublishedAt = &now
	}
	return nil
}

func (o *fakeOutbox) MarkDispatched(_ context.Context, seqs []int64) error {
	for _, seq := range seqs {
		now := o.db.now
		o.find(seq).DispatchedAt = &now
	}
	return nil
}

func (o *fakeOutbox) RecordFailure(_ context.Context, seq int64, message string, next time.Time) error {
	event := o.find(seq)
	event.Attempts++
	event.LastError = &message
	event.NextAttemptAt = next
	return nil
}

type fakeWebhooks struct{ db *fakeDB }

func (w *fakeWebhooks) ListMatching(_ context.Context, clientID string, instanceID uuid.UUID, eventType string) ([]domain.Webhook, error) {
	matching := []domain.Webhook{}
	for _, item := range w.db.webhooks {
		if item.ClientID == clientID && item.Active && matchesEvent(item, eventType) {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

func matchesEvent(item domain.Webhook, eventType string) bool {
	if len(item.Events) == 0 {
		return true
	}
	for _, subscribed := range item.Events {
		if subscribed == eventType {
			return true
		}
	}
	return false
}

type fakeDeliveries struct{ db *fakeDB }

func (f *fakeDeliveries) Insert(_ context.Context, delivery *domain.WebhookDelivery) (bool, error) {
	for _, existing := range f.db.deliveries {
		if existing.WebhookID == delivery.WebhookID && existing.EventID == delivery.EventID {
			return false, nil
		}
	}
	f.db.deliveries = append(f.db.deliveries, *delivery)
	return true, nil
}

type fakeInstances struct{ db *fakeDB }

func (i *fakeInstances) FindUnscoped(_ context.Context, id uuid.UUID) (*domain.Instance, error) {
	instance, found := i.db.instances[id]
	if !found {
		return nil, domain.InstanceNotFound()
	}
	return instance, nil
}

type fakePublisher struct {
	mu       sync.Mutex
	messages []Message
	failOn   map[string]error
}

func (p *fakePublisher) Publish(_ context.Context, message Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, failing := p.failOn[message.Key]; failing {
		return err
	}
	p.messages = append(p.messages, message)
	return nil
}

var errBrokerDown = errors.New("broker unavailable")

type blockingPublisher struct{}

func (blockingPublisher) Publish(ctx context.Context, _ Message) error {
	<-ctx.Done()
	return ctx.Err()
}
