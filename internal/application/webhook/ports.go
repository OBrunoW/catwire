package webhook

import (
	"context"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type WebhookStore interface {
	Insert(ctx context.Context, webhook *domain.Webhook) error
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Webhook, error)
	ListByClient(ctx context.Context, clientID string, active *bool, params apihttp.PageParams) (apihttp.PageResult[domain.Webhook], error)
	Update(ctx context.Context, clientID string, id uuid.UUID, update domain.WebhookUpdate) (*domain.Webhook, error)
	RotateSecret(ctx context.Context, clientID string, id uuid.UUID, secret string) error
	SoftDelete(ctx context.Context, clientID string, id uuid.UUID) error
}

type DeliveryStore interface {
	Insert(ctx context.Context, delivery *domain.WebhookDelivery) (bool, error)
	ListByWebhook(
		ctx context.Context,
		clientID string,
		webhookID uuid.UUID,
		filter domain.DeliveryFilter,
		params apihttp.PageParams,
	) (apihttp.PageResult[domain.WebhookDelivery], error)
	ResetForRetry(ctx context.Context, clientID string, webhookID, id uuid.UUID) (*domain.WebhookDelivery, error)
}

type InstanceReader interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
}

type DispatchStore interface {
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.DueDelivery, error)
	RecordSuccess(ctx context.Context, id uuid.UUID, statusCode int) error
	RecordFailure(
		ctx context.Context,
		id uuid.UUID,
		statusCode *int,
		message string,
		nextAttemptAt time.Time,
		maxAttempts int,
	) (string, error)
}

type Request struct {
	URL        string
	Secret     string
	DeliveryID uuid.UUID
	EventID    uuid.UUID
	EventType  string
	Body       []byte
	Timestamp  time.Time
}

type Sender interface {
	Send(ctx context.Context, request Request) (int, error)
}
