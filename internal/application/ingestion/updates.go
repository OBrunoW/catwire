package ingestion

import (
	"context"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func (s *Service) IngestEdit(ctx context.Context, instance domain.Instance, change EditChange) error {
	return s.applyChange(ctx, instance, change.Chat, change.TargetWAMessageID, func(unit Unit, target domain.Message) error {
		applied, err := unit.Messages.ApplyEdit(ctx, instance.ID, target.ID, change.Content.Text, change.Content.Data, change.At)
		if err != nil || !applied {
			return err
		}
		return s.emitUpdated(ctx, unit, instance, target.ID, ChangeEdited, nil)
	})
}

func (s *Service) IngestRevocation(ctx context.Context, instance domain.Instance, change RevocationChange) error {
	return s.applyChange(ctx, instance, change.Chat, change.TargetWAMessageID, func(unit Unit, target domain.Message) error {
		applied, err := unit.Messages.ApplyRevocation(ctx, instance.ID, target.ID, change.At)
		if err != nil || !applied {
			return err
		}
		return s.emitUpdated(ctx, unit, instance, target.ID, ChangeRevoked, nil)
	})
}

func (s *Service) IngestReaction(ctx context.Context, instance domain.Instance, change ReactionChange) error {
	return s.applyChange(ctx, instance, change.Chat, change.TargetWAMessageID, func(unit Unit, target domain.Message) error {
		removed := change.Emoji == ""
		reaction := domain.Reaction{SenderJID: change.SenderJID, Emoji: change.Emoji, ReactedAt: change.At}
		applied, err := unit.Messages.ApplyReaction(ctx, instance.ID, target.ID, reaction, removed)
		if err != nil || !applied {
			return err
		}
		resource := &ReactionResource{
			SenderJID: change.SenderJID,
			Emoji:     change.Emoji,
			ReactedAt: change.At,
			Removed:   removed,
		}
		return s.emitUpdated(ctx, unit, instance, target.ID, ChangeReaction, resource)
	})
}

func (s *Service) applyChange(
	ctx context.Context,
	instance domain.Instance,
	chat ChatRef,
	targetWAMessageID string,
	apply func(unit Unit, target domain.Message) error,
) error {
	settings, err := s.settings.FindByInstanceID(ctx, instance.ID)
	if err != nil {
		return err
	}
	if ignoredGroup(settings, chat) {
		return nil
	}
	return s.runUnit(ctx, func(unit Unit) error {
		target, err := unit.Messages.FindByWAMessageID(ctx, instance.ID, targetWAMessageID)
		if isNotFound(err, domain.MessageNotFound().Code) {
			s.logger.Warn("message change dropped: target unknown",
				"instance_id", instance.ID, "wa_message_id", targetWAMessageID)
			return nil
		}
		if err != nil {
			return err
		}
		return apply(unit, *target)
	})
}

func (s *Service) emitUpdated(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	messageID uuid.UUID,
	change string,
	reaction *ReactionResource,
) error {
	message, err := unit.Messages.FindByID(ctx, instance.ID, messageID)
	if err != nil {
		return err
	}
	chat, err := unit.Chats.FindByID(ctx, instance.ID, message.ChatID)
	if err != nil {
		return err
	}
	media, err := findMedia(ctx, unit.Media, message.ID)
	if err != nil {
		return err
	}
	replyTarget, err := findReplyTarget(ctx, unit.Messages, *message)
	if err != nil {
		return err
	}
	data := MessageUpdatedData{
		Change:   change,
		Message:  NewMessageResource(*message, chat.JID, media, replyTarget),
		Reaction: reaction,
	}
	return s.emit(ctx, unit, instance, EventTypeMessageUpdated, chatPartitionKey(instance.ID, chat.JID), data)
}

func findMedia(ctx context.Context, store MediaStore, messageID uuid.UUID) (*domain.Media, error) {
	items, err := store.FindByMessageIDs(ctx, []uuid.UUID{messageID})
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) IngestReceipt(ctx context.Context, instance domain.Instance, change ReceiptChange) error {
	settings, err := s.settings.FindByInstanceID(ctx, instance.ID)
	if err != nil {
		return err
	}
	if ignoredGroup(settings, change.Chat) {
		return nil
	}
	return s.runUnit(ctx, func(unit Unit) error {
		for _, waMessageID := range change.WAMessageIDs {
			if err := s.advanceStatus(ctx, unit, instance, waMessageID, change); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) advanceStatus(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	waMessageID string,
	change ReceiptChange,
) error {
	advances, err := unit.Messages.AdvanceStatusByWAMessageID(ctx, instance.ID, waMessageID, change.Status, change.At)
	if err != nil {
		return err
	}
	for _, advance := range advances {
		if err := s.emitStatusUpdated(ctx, unit, instance, advance, change); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) emitStatusUpdated(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	advance domain.StatusAdvance,
	change ReceiptChange,
) error {
	message, err := unit.Messages.FindByID(ctx, instance.ID, advance.MessageID)
	if err != nil {
		return err
	}
	chat, err := unit.Chats.FindByID(ctx, instance.ID, advance.ChatID)
	if err != nil {
		return err
	}
	data := StatusUpdatedData{
		MessageID:      message.ID,
		WAMessageID:    message.WAMessageID,
		ChatID:         chat.ID,
		ChatJID:        chat.JID,
		Direction:      message.Direction,
		Status:         change.Status,
		PreviousStatus: advance.PreviousStatus,
		StatusAt:       change.At,
		Metadata:       rawOr(message.Metadata, "{}"),
	}
	return s.emit(ctx, unit, instance, EventTypeMessageStatusUpdated, chatPartitionKey(instance.ID, chat.JID), data)
}
