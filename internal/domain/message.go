package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"

	MessageStatusPending   = "pending"
	MessageStatusSent      = "sent"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
	MessageStatusPlayed    = "played"
	MessageStatusFailed    = "failed"
	MessageStatusReceived  = "received"

	MessageTypeUndecryptable = "undecryptable"
)

type Message struct {
	ID                 uuid.UUID
	InstanceID         uuid.UUID
	ChatID             uuid.UUID
	WAMessageID        string
	Direction          string
	Origin             string
	Source             string
	SenderJID          string
	SenderPhone        *string
	SenderLID          *string
	SenderPushName     *string
	Type               string
	Text               *string
	Content            json.RawMessage
	ReplyToWAMessageID *string
	IsForwarded        bool
	IsEphemeral        bool
	IsViewOnce         bool
	Status             string
	ErrorCode          *string
	ErrorMessage       *string
	Reactions          json.RawMessage
	Metadata           json.RawMessage
	IdempotencyKey     *string
	RequestHash        *string
	SendAttempts       int
	NextAttemptAt      *time.Time
	Timestamp          time.Time
	SentAt             *time.Time
	DeliveredAt        *time.Time
	ReadAt             *time.Time
	PlayedAt           *time.Time
	EditedAt           *time.Time
	RevokedAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type MessageFilter struct {
	ChatID      *uuid.UUID
	WAMessageID string
	Direction   string
	Type        string
	Source      string
	Since       *time.Time
	Until       *time.Time
	Search      string
}

type StatusAdvance struct {
	MessageID      uuid.UUID
	ChatID         uuid.UUID
	PreviousStatus string
}

type MessageContentUpdate struct {
	Type    string
	Text    *string
	Content json.RawMessage
}

type Reaction struct {
	SenderJID string    `json:"sender_jid"`
	Emoji     string    `json:"emoji"`
	ReactedAt time.Time `json:"reacted_at"`
}
