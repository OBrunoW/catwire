package query

import (
	"context"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

type Options struct {
	Instances InstanceFinder
	Messages  MessageReader
	Chats     ChatReader
	Media     MediaReader
	URLs      *media.URLBuilder
}

type Service struct {
	instances InstanceFinder
	messages  MessageReader
	chats     ChatReader
	media     MediaReader
	urls      *media.URLBuilder
}

func NewService(options Options) *Service {
	return &Service{
		instances: options.Instances,
		messages:  options.Messages,
		chats:     options.Chats,
		media:     options.Media,
		urls:      options.URLs,
	}
}

func (s *Service) ListMessages(
	ctx context.Context,
	clientID string,
	instanceID uuid.UUID,
	filter domain.MessageFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[ingestion.MessageResource], error) {
	if _, err := s.instances.FindByID(ctx, clientID, instanceID); err != nil {
		return apihttp.PageResult[ingestion.MessageResource]{}, err
	}
	if err := validateMessageFilter(filter); err != nil {
		return apihttp.PageResult[ingestion.MessageResource]{}, err
	}
	page, err := s.messages.List(ctx, instanceID, filter, params)
	if err != nil {
		return apihttp.PageResult[ingestion.MessageResource]{}, err
	}
	resources, err := s.messageResources(ctx, instanceID, page.Data)
	if err != nil {
		return apihttp.PageResult[ingestion.MessageResource]{}, err
	}
	return apihttp.PageResult[ingestion.MessageResource]{
		Data:       resources,
		Limit:      page.Limit,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

func (s *Service) GetMessage(ctx context.Context, clientID string, instanceID, id uuid.UUID) (ingestion.MessageResource, error) {
	if _, err := s.instances.FindByID(ctx, clientID, instanceID); err != nil {
		return ingestion.MessageResource{}, err
	}
	message, err := s.messages.FindByID(ctx, instanceID, id)
	if err != nil {
		return ingestion.MessageResource{}, err
	}
	resources, err := s.messageResources(ctx, instanceID, []domain.Message{*message})
	if err != nil {
		return ingestion.MessageResource{}, err
	}
	return resources[0], nil
}

func (s *Service) ListChats(
	ctx context.Context,
	clientID string,
	instanceID uuid.UUID,
	filter domain.ChatFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[ChatResource], error) {
	if _, err := s.instances.FindByID(ctx, clientID, instanceID); err != nil {
		return apihttp.PageResult[ChatResource]{}, err
	}
	if err := validateChatFilter(filter); err != nil {
		return apihttp.PageResult[ChatResource]{}, err
	}
	page, err := s.chats.ListByInstance(ctx, instanceID, filter, params)
	if err != nil {
		return apihttp.PageResult[ChatResource]{}, err
	}
	resources := make([]ChatResource, 0, len(page.Data))
	for _, view := range page.Data {
		resources = append(resources, newChatResource(view))
	}
	return apihttp.PageResult[ChatResource]{
		Data:       resources,
		Limit:      page.Limit,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

func (s *Service) GetChat(ctx context.Context, clientID string, instanceID, id uuid.UUID) (ChatResource, error) {
	if _, err := s.instances.FindByID(ctx, clientID, instanceID); err != nil {
		return ChatResource{}, err
	}
	view, err := s.chats.FindByID(ctx, instanceID, id)
	if err != nil {
		return ChatResource{}, err
	}
	return newChatResource(*view), nil
}
