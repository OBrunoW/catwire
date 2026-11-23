package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
)

type IngestionUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewIngestionUnitOfWork(pool *pgxpool.Pool) *IngestionUnitOfWork {
	return &IngestionUnitOfWork{pool: pool}
}

func (u *IngestionUnitOfWork) Do(ctx context.Context, run func(unit ingestion.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(ingestion.Unit{
			Chats:    NewChatRepository(tx),
			Messages: NewMessageRepository(tx),
			Media:    NewMediaRepository(tx),
			Outbox:   NewOutboxRepository(tx),
		})
	})
}

var _ ingestion.UnitOfWork = (*IngestionUnitOfWork)(nil)
var _ ingestion.SettingsReader = (*InstanceSettingsRepository)(nil)
