package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type Header struct {
	Key   string
	Value string
}

type Message struct {
	Topic   string
	Key     string
	Value   []byte
	Headers []Header
}

type Publisher interface {
	Publish(ctx context.Context, message Message) error
}

type EventStore interface {
	FetchPending(ctx context.Context, limit int) ([]domain.OutboxEvent, error)
	MarkKafkaPublished(ctx context.Context, seqs []int64) error
	MarkDispatched(ctx context.Context, seqs []int64) error
	RecordFailure(ctx context.Context, seq int64, message string, nextAttemptAt time.Time) error
}

type WebhookMatcher interface {
	ListMatching(ctx context.Context, clientID string, instanceID uuid.UUID, eventType string) ([]domain.Webhook, error)
}

type DeliveryWriter interface {
	Insert(ctx context.Context, delivery *domain.WebhookDelivery) (bool, error)
}

type InstanceReader interface {
	FindUnscoped(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
}

type Unit struct {
	Outbox     EventStore
	Webhooks   WebhookMatcher
	Deliveries DeliveryWriter
	Instances  InstanceReader
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}

type RetentionStore interface {
	DeleteDispatchedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
