package ingestion

import (
	"context"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func resolveChat(ctx context.Context, chats ChatStore, instanceID uuid.UUID, ref ChatRef) (domain.Chat, error) {
	existing, err := chats.FindByJID(ctx, instanceID, ref.JID)
	if err == nil {
		return linkLID(ctx, chats, instanceID, *existing, ref)
	}
	if !isNotFound(err, domain.ChatNotFound().Code) {
		return domain.Chat{}, err
	}
	migrated, found, err := migrateLIDChat(ctx, chats, instanceID, ref)
	if err != nil || found {
		return migrated, err
	}
	return createChat(ctx, chats, instanceID, ref)
}

func linkLID(ctx context.Context, chats ChatStore, instanceID uuid.UUID, existing domain.Chat, ref ChatRef) (domain.Chat, error) {
	if ref.LID == nil || existing.LID != nil {
		return existing, nil
	}
	_, err := chats.FindByLID(ctx, instanceID, *ref.LID)
	if err == nil {
		return existing, nil
	}
	if !isNotFound(err, domain.ChatNotFound().Code) {
		return domain.Chat{}, err
	}
	existing.LID = ref.LID
	if err := chats.Upsert(ctx, &existing); err != nil {
		return domain.Chat{}, err
	}
	return existing, nil
}

func migrateLIDChat(ctx context.Context, chats ChatStore, instanceID uuid.UUID, ref ChatRef) (domain.Chat, bool, error) {
	if ref.LID == nil {
		return domain.Chat{}, false, nil
	}
	byLID, err := chats.FindByLID(ctx, instanceID, *ref.LID)
	if isNotFound(err, domain.ChatNotFound().Code) {
		return domain.Chat{}, false, nil
	}
	if err != nil {
		return domain.Chat{}, false, err
	}
	if ref.Phone == nil {
		return *byLID, true, nil
	}
	if err := chats.UpdateJID(ctx, instanceID, byLID.ID, ref.JID, ref.Phone); err != nil {
		return domain.Chat{}, false, err
	}
	byLID.JID = ref.JID
	byLID.Phone = ref.Phone
	return *byLID, true, nil
}

func createChat(ctx context.Context, chats ChatStore, instanceID uuid.UUID, ref ChatRef) (domain.Chat, error) {
	chat := domain.Chat{
		ID:         uuid.New(),
		InstanceID: instanceID,
		JID:        ref.JID,
		LID:        ref.LID,
		Phone:      ref.Phone,
		Type:       domain.ChatTypeDirect,
		Name:       ref.Name,
	}
	if ref.Group {
		chat.Type = domain.ChatTypeGroup
	}
	if err := chats.Upsert(ctx, &chat); err != nil {
		return domain.Chat{}, err
	}
	return chat, nil
}
