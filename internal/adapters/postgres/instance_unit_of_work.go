package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/application/instance"
)

type InstanceUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewInstanceUnitOfWork(pool *pgxpool.Pool) *InstanceUnitOfWork {
	return &InstanceUnitOfWork{pool: pool}
}

func (u *InstanceUnitOfWork) Do(ctx context.Context, run func(unit instance.Unit) error) error {
	return WithTx(ctx, u.pool, func(tx DBTX) error {
		return run(instance.Unit{
			Instances: NewInstanceRepository(tx),
			Settings:  NewInstanceSettingsRepository(tx),
		})
	})
}

var (
	_ instance.UnitOfWork    = (*InstanceUnitOfWork)(nil)
	_ instance.InstanceStore = (*InstanceRepository)(nil)
	_ instance.SettingsStore = (*InstanceSettingsRepository)(nil)
)
