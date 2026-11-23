package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

func (s *Service) messageResources(
	ctx context.Context,
	instanceID uuid.UUID,
	messages []domain.Message,
) ([]ingestion.MessageResource, error) {
	if len(messages) == 0 {
		return []ingestion.MessageResource{}, nil
	}
	jids, err := s.chats.FindJIDsByIDs(ctx, instanceID, distinctChatIDs(messages))
	if err != nil {
		return nil, err
	}
	attachments, err := s.attachmentsByMessage(ctx, messages)
	if err != nil {
		return nil, err
	}
	targets, err := s.replyTargets(ctx, instanceID, messages)
	if err != nil {
		return nil, err
	}
	resources := make([]ingestion.MessageResource, 0, len(messages))
	for _, message := range messages {
		resources = append(resources, s.messageResource(message, jids, attachments, targets))
	}
	return resources, nil
}

func (s *Service) messageResource(
	message domain.Message,
	jids map[uuid.UUID]string,
	attachments map[uuid.UUID]domain.Media,
	targets map[domain.MessageRef]uuid.UUID,
) ingestion.MessageResource {
	var replyTarget *uuid.UUID
	if message.ReplyToWAMessageID != nil {
		if found, ok := targets[domain.MessageRef{ChatID: message.ChatID, WAMessageID: *message.ReplyToWAMessageID}]; ok {
			replyTarget = &found
		}
	}
	attachment, hasMedia := attachments[message.ID]
	var attached *domain.Media
	if hasMedia {
		attached = &attachment
	}
	resource := ingestion.NewMessageResource(message, jids[message.ChatID], attached, replyTarget)
	if hasMedia {
		built := s.urls.Resource(attachment)
		resource.Media = &built
	}
	return resource
}

func distinctChatIDs(messages []domain.Message) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(messages))
	ids := make([]uuid.UUID, 0, len(messages))
	for _, message := range messages {
		if seen[message.ChatID] {
			continue
		}
		seen[message.ChatID] = true
		ids = append(ids, message.ChatID)
	}
	return ids
}

func (s *Service) attachmentsByMessage(ctx context.Context, messages []domain.Message) (map[uuid.UUID]domain.Media, error) {
	ids := make([]uuid.UUID, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	items, err := s.media.FindByMessageIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byMessage := make(map[uuid.UUID]domain.Media, len(items))
	for _, item := range items {
		if item.MessageID != nil {
			byMessage[*item.MessageID] = item
		}
	}
	return byMessage, nil
}

func (s *Service) replyTargets(
	ctx context.Context,
	instanceID uuid.UUID,
	messages []domain.Message,
) (map[domain.MessageRef]uuid.UUID, error) {
	references := make([]domain.MessageRef, 0)
	for _, message := range messages {
		if message.ReplyToWAMessageID != nil {
			references = append(references, domain.MessageRef{ChatID: message.ChatID, WAMessageID: *message.ReplyToWAMessageID})
		}
	}
	if len(references) == 0 {
		return map[domain.MessageRef]uuid.UUID{}, nil
	}
	return s.messages.FindReplyTargets(ctx, instanceID, references)
}
