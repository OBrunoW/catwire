package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/outbound"
)

type OutboundUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewOutboundUnitOfWork(pool *pgxpool.Pool) *OutboundUnitOfWork {
	return &OutboundUnitOfWork{pool: pool}
}

func (u *OutboundUnitOfWork) Do(ctx context.Context, run func(unit outbound.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(outbound.Unit{
			Instances: NewInstanceRepository(tx),
			Chats:     NewChatRepository(tx),
			Messages:  NewMessageRepository(tx),
			Media:     NewMediaRepository(tx),
			Outbox:    NewOutboxRepository(tx),
		})
	})
}

var (
	_ outbound.UnitOfWork    = (*OutboundUnitOfWork)(nil)
	_ outbound.InstanceStore = (*InstanceRepository)(nil)
	_ outbound.ChatStore     = (*ChatRepository)(nil)
	_ outbound.MessageStore  = (*MessageRepository)(nil)
	_ outbound.MediaStore    = (*MediaRepository)(nil)
	_ outbound.OutboxWriter  = (*OutboxRepository)(nil)
)
