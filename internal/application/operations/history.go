package operations

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	minHistoryCount = 1
	maxHistoryCount = 50
)

type HistoryResult struct {
	RequestID string
	ChatID    uuid.UUID
}

type HistoryService struct {
	instances InstanceStore
	chats     ChatStore
	messages  HistoryMessageStore
	gateway   HistoryGateway
	logger    *slog.Logger
}

func NewHistoryService(
	instances InstanceStore,
	chats ChatStore,
	messages HistoryMessageStore,
	gateway HistoryGateway,
	logger *slog.Logger,
) *HistoryService {
	return &HistoryService{instances: instances, chats: chats, messages: messages, gateway: gateway, logger: logger}
}

func (s *HistoryService) Request(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, count int) (HistoryResult, error) {
	if count < minHistoryCount || count > maxHistoryCount {
		return HistoryResult{}, domain.BadRequest("count must be between 1 and 50")
	}
	instance, err := findConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return HistoryResult{}, err
	}
	view, err := s.chats.FindByID(ctx, instance.ID, chatID)
	if err != nil {
		return HistoryResult{}, err
	}
	anchor, err := s.anchor(ctx, instance.ID, chatID)
	if err != nil {
		return HistoryResult{}, err
	}
	request := HistoryRequest{
		ChatJID:           view.JID,
		OldestWAMessageID: anchor.WAMessageID,
		OldestFromMe:      anchor.Direction == domain.DirectionOutbound,
		OldestTimestamp:   anchor.Timestamp,
		Count:             count,
	}
	requestID, err := s.gateway.RequestHistory(ctx, instance.ID, request)
	if err != nil {
		return HistoryResult{}, gatewayFailure(s.logger, "request history", instance.ID, err)
	}
	return HistoryResult{RequestID: requestID, ChatID: chatID}, nil
}

func (s *HistoryService) anchor(ctx context.Context, instanceID, chatID uuid.UUID) (*domain.Message, error) {
	anchor, err := s.messages.FindOldestInChat(ctx, instanceID, chatID)
	if err == nil {
		return anchor, nil
	}
	if appErr, ok := domain.AsAppError(err); ok && appErr.Code == domain.MessageNotFound().Code {
		return nil, domain.HistoryUnavailable()
	}
	return nil, fmt.Errorf("operations: find history anchor: %w", err)
}
