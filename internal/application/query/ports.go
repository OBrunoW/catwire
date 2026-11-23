package query

import (
	"context"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type InstanceFinder interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
}

type MessageReader interface {
	List(
		ctx context.Context,
		instanceID uuid.UUID,
		filter domain.MessageFilter,
		params apihttp.PageParams,
	) (apihttp.PageResult[domain.Message], error)
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.Message, error)
	FindReplyTargets(ctx context.Context, instanceID uuid.UUID, references []domain.MessageRef) (map[domain.MessageRef]uuid.UUID, error)
}

type ChatReader interface {
	ListByInstance(
		ctx context.Context,
		instanceID uuid.UUID,
		filter domain.ChatFilter,
		params apihttp.PageParams,
	) (apihttp.PageResult[domain.ChatView], error)
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error)
	FindJIDsByIDs(ctx context.Context, instanceID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

type MediaReader interface {
	FindByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) ([]domain.Media, error)
}
