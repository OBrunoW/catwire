package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/outbox"
	"github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/domain"
)

type OutboxUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewOutboxUnitOfWork(pool *pgxpool.Pool) *OutboxUnitOfWork {
	return &OutboxUnitOfWork{pool: pool}
}

type lockedOutbox struct {
	*OutboxRepository
	tx DBTX
}

func (o lockedOutbox) FetchPending(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	return o.OutboxRepository.FetchPending(ctx, o.tx, limit)
}

func (u *OutboxUnitOfWork) Do(ctx context.Context, run func(unit outbox.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(outbox.Unit{
			Outbox:     lockedOutbox{OutboxRepository: NewOutboxRepository(tx), tx: tx},
			Webhooks:   NewWebhookRepository(tx),
			Deliveries: NewWebhookDeliveryRepository(tx),
			Instances:  NewInstanceRepository(tx),
		})
	})
}

var (
	_ outbox.UnitOfWork      = (*OutboxUnitOfWork)(nil)
	_ outbox.RetentionStore  = (*OutboxRepository)(nil)
	_ webhook.WebhookStore   = (*WebhookRepository)(nil)
	_ webhook.DeliveryStore  = (*WebhookDeliveryRepository)(nil)
	_ webhook.DispatchStore  = (*WebhookDeliveryRepository)(nil)
	_ webhook.InstanceReader = (*InstanceRepository)(nil)
)
