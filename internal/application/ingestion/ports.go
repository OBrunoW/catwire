package ingestion

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type ChatStore interface {
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error)
	FindByJID(ctx context.Context, instanceID uuid.UUID, jid string) (*domain.Chat, error)
	FindByLID(ctx context.Context, instanceID uuid.UUID, lid string) (*domain.Chat, error)
	Upsert(ctx context.Context, chat *domain.Chat) error
	UpdateJID(ctx context.Context, instanceID, id uuid.UUID, jid string, phone *string) error
	TouchLastMessage(ctx context.Context, instanceID, id, messageID uuid.UUID, at time.Time) error
}

type MessageStore interface {
	Insert(ctx context.Context, message *domain.Message) (bool, error)
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.Message, error)
	FindByWAMessageID(ctx context.Context, instanceID uuid.UUID, waMessageID string) (*domain.Message, error)
	FindByChatAndWAMessageID(ctx context.Context, instanceID, chatID uuid.UUID, waMessageID string) (*domain.Message, error)
	AdvanceStatusByWAMessageID(
		ctx context.Context,
		instanceID uuid.UUID,
		waMessageID string,
		status string,
		at time.Time,
	) ([]domain.StatusAdvance, error)
	ReplaceUndecryptable(ctx context.Context, instanceID, id uuid.UUID, update domain.MessageContentUpdate) (bool, error)
	ApplyEdit(ctx context.Context, instanceID, id uuid.UUID, text *string, content json.RawMessage, at time.Time) (bool, error)
	ApplyRevocation(ctx context.Context, instanceID, id uuid.UUID, at time.Time) (bool, error)
	ApplyReaction(ctx context.Context, instanceID, id uuid.UUID, reaction domain.Reaction, removed bool) (bool, error)
}

type MediaStore interface {
	Insert(ctx context.Context, media *domain.Media) error
	FindByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) ([]domain.Media, error)
}

type OutboxWriter interface {
	Insert(ctx context.Context, event *domain.OutboxEvent) error
}

type SettingsReader interface {
	FindByInstanceID(ctx context.Context, instanceID uuid.UUID) (*domain.InstanceSettings, error)
}

type Unit struct {
	Chats    ChatStore
	Messages MessageStore
	Media    MediaStore
	Outbox   OutboxWriter
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}
