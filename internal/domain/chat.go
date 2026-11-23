package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ChatTypeDirect = "direct"
	ChatTypeGroup  = "group"
)

type Chat struct {
	ID            uuid.UUID
	InstanceID    uuid.UUID
	JID           string
	LID           *string
	Phone         *string
	Type          string
	Name          *string
	LastMessageID *uuid.UUID
	LastMessageAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type LastMessageSummary struct {
	ID        uuid.UUID
	Type      string
	Text      *string
	Direction string
	Timestamp time.Time
}

type ChatView struct {
	Chat
	LastMessage *LastMessageSummary
}

type ChatFilter struct {
	Search string
	Type   string
}
