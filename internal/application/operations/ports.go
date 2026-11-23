package operations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var ErrInstanceDisconnected = errors.New("operations: instance is not connected")

type InstanceStore interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
}

type ChatStore interface {
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error)
}

type ProfileData struct {
	PushName      string
	StatusMessage string
	PictureURL    *string
}

type ProfileGateway interface {
	GetProfile(ctx context.Context, instanceID uuid.UUID) (ProfileData, error)
	SetStatusMessage(ctx context.Context, instanceID uuid.UUID, text string) error
	SetPushName(ctx context.Context, instanceID uuid.UUID, name string) error
}

type PrivacyChange struct {
	Key   string
	Value string
}

type PrivacyGateway interface {
	GetPrivacy(ctx context.Context, instanceID uuid.UUID) (PrivacySettings, error)
	SetPrivacy(ctx context.Context, instanceID uuid.UUID, change PrivacyChange) (PrivacySettings, error)
}

type PresenceGateway interface {
	SendPresence(ctx context.Context, instanceID uuid.UUID, chatJID, state string) error
}

type ReadReceipt struct {
	ChatJID      string
	SenderJID    string
	WAMessageIDs []string
	ReadAt       time.Time
}

type ReadGateway interface {
	MarkRead(ctx context.Context, instanceID uuid.UUID, receipt ReadReceipt) error
}

type ReadMessageStore interface {
	ListInboundReceived(ctx context.Context, instanceID, chatID uuid.UUID) ([]domain.Message, error)
	FindByChatAndIDs(ctx context.Context, instanceID, chatID uuid.UUID, ids []uuid.UUID) ([]domain.Message, error)
	AdvanceStatus(ctx context.Context, instanceID, id uuid.UUID, status string, at time.Time) (domain.StatusAdvance, bool, error)
}

type HistoryRequest struct {
	ChatJID           string
	OldestWAMessageID string
	OldestFromMe      bool
	OldestTimestamp   time.Time
	Count             int
}

type HistoryGateway interface {
	RequestHistory(ctx context.Context, instanceID uuid.UUID, request HistoryRequest) (string, error)
}

type HistoryMessageStore interface {
	FindOldestInChat(ctx context.Context, instanceID, chatID uuid.UUID) (*domain.Message, error)
}
