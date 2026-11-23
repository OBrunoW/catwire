package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

const EventTypeMessageFailed = "whatsapp.message.failed"

type MessageView struct {
	Message     domain.Message
	ChatJID     string
	Media       *domain.Media
	ReplyTarget *uuid.UUID
}

func (v MessageView) Resource() ingestion.MessageResource {
	return ingestion.NewMessageResource(v.Message, v.ChatJID, v.Media, v.ReplyTarget)
}

func loadView(ctx context.Context, unit Unit, message domain.Message) (MessageView, error) {
	chat, err := unit.Chats.FindByID(ctx, message.InstanceID, message.ChatID)
	if err != nil {
		return MessageView{}, err
	}
	items, err := unit.Media.FindByMessageIDs(ctx, []uuid.UUID{message.ID})
	if err != nil {
		return MessageView{}, err
	}
	replyTarget, err := findReplyTarget(ctx, unit.Messages, message)
	if err != nil {
		return MessageView{}, err
	}
	view := MessageView{Message: message, ChatJID: chat.JID, ReplyTarget: replyTarget}
	if len(items) > 0 {
		view.Media = &items[0]
	}
	return view, nil
}

func findReplyTarget(ctx context.Context, store MessageStore, message domain.Message) (*uuid.UUID, error) {
	if message.ReplyToWAMessageID == nil {
		return nil, nil
	}
	target, err := store.FindByChatAndWAMessageID(ctx, message.InstanceID, message.ChatID, *message.ReplyToWAMessageID)
	if isMessageNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &target.ID, nil
}

func isMessageNotFound(err error) bool {
	var appError *domain.AppError
	return errors.As(err, &appError) && appError.Code == domain.MessageNotFound().Code
}

func newMessageEvent(instance domain.Instance, eventType string, view MessageView, occurredAt time.Time) (*domain.OutboxEvent, error) {
	payload, err := json.Marshal(ingestion.MessageEventData{Message: view.Resource()})
	if err != nil {
		return nil, fmt.Errorf("outbound: encode %s payload: %w", eventType, err)
	}
	instanceID := instance.ID
	return &domain.OutboxEvent{
		EventID:      uuid.New(),
		EventType:    eventType,
		EventVersion: 1,
		ClientID:     instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: instance.ID.String() + ":" + view.ChatJID,
		Payload:      payload,
		OccurredAt:   occurredAt,
	}, nil
}
