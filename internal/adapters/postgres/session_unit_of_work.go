package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/session"
)

type SessionUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewSessionUnitOfWork(pool *pgxpool.Pool) *SessionUnitOfWork {
	return &SessionUnitOfWork{pool: pool}
}

func (u *SessionUnitOfWork) Do(ctx context.Context, run func(unit session.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(session.Unit{
			Instances: NewInstanceRepository(tx),
			Outbox:    NewOutboxRepository(tx),
		})
	})
}

var _ session.UnitOfWork = (*SessionUnitOfWork)(nil)
