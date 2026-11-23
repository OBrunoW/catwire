package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type Options struct {
	Unit     UnitOfWork
	Settings SettingsReader
	Logger   *slog.Logger
	Now      func() time.Time
}

type Service struct {
	unit     UnitOfWork
	settings SettingsReader
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(options Options) *Service {
	service := &Service{
		unit:     options.Unit,
		settings: options.Settings,
		logger:   options.Logger,
		now:      options.Now,
	}
	if service.logger == nil {
		service.logger = slog.Default()
	}
	if service.now == nil {
		service.now = time.Now
	}
	return service
}

func (s *Service) IngestMessage(ctx context.Context, instance domain.Instance, incoming IncomingMessage) error {
	settings, err := s.settings.FindByInstanceID(ctx, instance.ID)
	if err != nil {
		return err
	}
	if ignoredGroup(settings, incoming.Chat) {
		return nil
	}
	mediaStatus := domain.MediaStatusPending
	if !settings.DownloadMedia {
		mediaStatus = domain.MediaStatusSkipped
	}
	return s.runUnit(ctx, func(unit Unit) error {
		chat, err := resolveChat(ctx, unit.Chats, instance.ID, incoming.Chat)
		if err != nil {
			return err
		}
		stored, err := s.storeMessage(ctx, unit, instance, chat, incoming, mediaStatus)
		if err != nil {
			return err
		}
		if !stored.changed() {
			return nil
		}
		return s.emitMessage(ctx, unit, instance, stored)
	})
}

func (s *Service) runUnit(ctx context.Context, run func(unit Unit) error) error {
	err := s.unit.Do(ctx, run)
	if !errors.Is(err, domain.ErrChatIdentityConflict) {
		return err
	}
	if err := s.unit.Do(ctx, run); err != nil {
		return fmt.Errorf("run unit after chat identity conflict: %w", err)
	}
	return nil
}

func ignoredGroup(settings *domain.InstanceSettings, chat ChatRef) bool {
	return chat.Group && settings.IgnoreGroups
}

type storedMessage struct {
	message  domain.Message
	chat     domain.Chat
	media    *domain.Media
	inserted bool
	replaced bool
}

func (r storedMessage) changed() bool {
	return r.inserted || r.replaced
}

func (s *Service) storeMessage(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	chat domain.Chat,
	incoming IncomingMessage,
	mediaStatus string,
) (storedMessage, error) {
	message := buildMessage(instance.ID, chat.ID, incoming)
	inserted, err := unit.Messages.Insert(ctx, &message)
	if err != nil {
		return storedMessage{}, err
	}
	if !inserted {
		return s.replaceUndecryptable(ctx, unit, instance, chat, incoming, mediaStatus)
	}
	media, err := insertMedia(ctx, unit.Media, instance.ID, message.ID, incoming.Content.Media, mediaStatus)
	if err != nil {
		return storedMessage{}, err
	}
	if err := unit.Chats.TouchLastMessage(ctx, instance.ID, chat.ID, message.ID, message.Timestamp); err != nil {
		return storedMessage{}, err
	}
	return storedMessage{message: message, chat: chat, media: media, inserted: true}, nil
}

func (s *Service) replaceUndecryptable(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	chat domain.Chat,
	incoming IncomingMessage,
	mediaStatus string,
) (storedMessage, error) {
	if incoming.Source == SourceHistory || incoming.Content.Type == MessageTypeUndecryptable {
		return storedMessage{}, nil
	}
	existing, err := unit.Messages.FindByChatAndWAMessageID(ctx, instance.ID, chat.ID, incoming.WAMessageID)
	if err != nil {
		return storedMessage{}, err
	}
	if existing.Type != MessageTypeUndecryptable {
		return storedMessage{}, nil
	}
	update := domain.MessageContentUpdate{
		Type:    incoming.Content.Type,
		Text:    incoming.Content.Text,
		Content: incoming.Content.Data,
	}
	replaced, err := unit.Messages.ReplaceUndecryptable(ctx, instance.ID, existing.ID, update)
	if err != nil || !replaced {
		return storedMessage{}, err
	}
	existing.Type = update.Type
	existing.Text = update.Text
	existing.Content = update.Content
	media, err := insertMedia(ctx, unit.Media, instance.ID, existing.ID, incoming.Content.Media, mediaStatus)
	if err != nil {
		return storedMessage{}, err
	}
	return storedMessage{message: *existing, chat: chat, media: media, replaced: true}, nil
}

func buildMessage(instanceID, chatID uuid.UUID, incoming IncomingMessage) domain.Message {
	message := domain.Message{
		ID:                 uuid.New(),
		InstanceID:         instanceID,
		ChatID:             chatID,
		WAMessageID:        incoming.WAMessageID,
		Direction:          domain.DirectionInbound,
		Origin:             OriginContact,
		Source:             incoming.Source,
		SenderJID:          incoming.Sender.JID,
		SenderPhone:        incoming.Sender.Phone,
		SenderLID:          incoming.Sender.LID,
		SenderPushName:     incoming.Sender.PushName,
		Type:               incoming.Content.Type,
		Text:               incoming.Content.Text,
		Content:            incoming.Content.Data,
		ReplyToWAMessageID: incoming.Content.ReplyToWAMessageID,
		IsForwarded:        incoming.Content.IsForwarded,
		IsEphemeral:        incoming.Content.IsEphemeral,
		IsViewOnce:         incoming.Content.IsViewOnce,
		Status:             domain.MessageStatusReceived,
		Timestamp:          incoming.Timestamp,
	}
	if incoming.FromMe {
		sentAt := incoming.Timestamp
		message.Direction = domain.DirectionOutbound
		message.Origin = OriginDevice
		message.Status = domain.MessageStatusSent
		message.SentAt = &sentAt
	}
	return message
}

func insertMedia(
	ctx context.Context,
	store MediaStore,
	instanceID, messageID uuid.UUID,
	descriptor *MediaDescriptor,
	status string,
) (*domain.Media, error) {
	if descriptor == nil {
		return nil, nil
	}
	media := domain.Media{
		ID:              uuid.New(),
		InstanceID:      instanceID,
		MessageID:       &messageID,
		Kind:            descriptor.Kind,
		Status:          status,
		MimeType:        descriptor.MimeType,
		FileName:        descriptor.FileName,
		SizeBytes:       descriptor.SizeBytes,
		Width:           descriptor.Width,
		Height:          descriptor.Height,
		DurationSeconds: descriptor.DurationSeconds,
		IsVoiceNote:     descriptor.IsVoiceNote,
		WADirectPath:    descriptor.DirectPath,
		WAMediaKey:      descriptor.MediaKey,
		WAFileSHA256:    descriptor.FileSHA256,
		WAFileEncSHA256: descriptor.FileEncSHA256,
	}
	if err := store.Insert(ctx, &media); err != nil {
		return nil, err
	}
	return &media, nil
}

func (s *Service) emitMessage(ctx context.Context, unit Unit, instance domain.Instance, stored storedMessage) error {
	replyTarget, err := findReplyTarget(ctx, unit.Messages, stored.message)
	if err != nil {
		return err
	}
	resource := NewMessageResource(stored.message, stored.chat.JID, stored.media, replyTarget)
	eventType := EventTypeMessageReceived
	if stored.message.Direction == domain.DirectionOutbound {
		eventType = EventTypeMessageSent
	}
	return s.emit(ctx, unit, instance, eventType, chatPartitionKey(instance.ID, stored.chat.JID), MessageEventData{Message: resource})
}

func findReplyTarget(ctx context.Context, store MessageStore, message domain.Message) (*uuid.UUID, error) {
	if message.ReplyToWAMessageID == nil {
		return nil, nil
	}
	target, err := store.FindByChatAndWAMessageID(ctx, message.InstanceID, message.ChatID, *message.ReplyToWAMessageID)
	if isNotFound(err, domain.MessageNotFound().Code) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &target.ID, nil
}

func (s *Service) emit(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	eventType, partitionKey string,
	data any,
) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("ingestion: encode %s payload: %w", eventType, err)
	}
	instanceID := instance.ID
	return unit.Outbox.Insert(ctx, &domain.OutboxEvent{
		EventID:      uuid.New(),
		EventType:    eventType,
		EventVersion: 1,
		ClientID:     instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: partitionKey,
		Payload:      payload,
		OccurredAt:   s.now(),
	})
}

func chatPartitionKey(instanceID uuid.UUID, chatJID string) string {
	return instanceID.String() + ":" + chatJID
}

func isNotFound(err error, code string) bool {
	var appError *domain.AppError
	return errors.As(err, &appError) && appError.Code == code
}
