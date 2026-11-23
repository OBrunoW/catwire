package instance

import (
	"context"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type InstanceStore interface {
	Insert(ctx context.Context, instance *domain.Instance) error
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
	ListByClient(
		ctx context.Context,
		clientID string,
		filter domain.InstanceFilter,
		params apihttp.PageParams,
	) (apihttp.PageResult[domain.Instance], error)
	Update(ctx context.Context, clientID string, id uuid.UUID, update domain.InstanceUpdate) (*domain.Instance, error)
}

type SettingsStore interface {
	Insert(ctx context.Context, settings *domain.InstanceSettings) error
	FindByInstanceID(ctx context.Context, instanceID uuid.UUID) (*domain.InstanceSettings, error)
	FindByInstanceIDs(ctx context.Context, instanceIDs []uuid.UUID) (map[uuid.UUID]domain.InstanceSettings, error)
	Update(ctx context.Context, settings *domain.InstanceSettings) error
}

type Unit struct {
	Instances InstanceStore
	Settings  SettingsStore
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}
