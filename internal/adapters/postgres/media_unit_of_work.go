package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/media"
)

type MediaUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewMediaUnitOfWork(pool *pgxpool.Pool) *MediaUnitOfWork {
	return &MediaUnitOfWork{pool: pool}
}

func (u *MediaUnitOfWork) Do(ctx context.Context, run func(unit media.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(media.Unit{
			Media:     NewMediaRepository(tx),
			Messages:  NewMessageRepository(tx),
			Chats:     NewChatRepository(tx),
			Instances: NewInstanceRepository(tx),
			Outbox:    NewOutboxRepository(tx),
		})
	})
}

var (
	_ media.UnitOfWork  = (*MediaUnitOfWork)(nil)
	_ media.MediaStore  = (*MediaRepository)(nil)
	_ media.MediaFinder = (*MediaRepository)(nil)
)
