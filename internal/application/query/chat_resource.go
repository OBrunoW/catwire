package query

import (
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type LastMessageResource struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	Text      *string   `json:"text"`
	Direction string    `json:"direction"`
	Timestamp time.Time `json:"timestamp"`
}

type ChatResource struct {
	ID            uuid.UUID            `json:"id"`
	InstanceID    uuid.UUID            `json:"instance_id"`
	JID           string               `json:"jid"`
	Phone         *string              `json:"phone"`
	LID           *string              `json:"lid"`
	Type          string               `json:"type"`
	Name          *string              `json:"name"`
	LastMessageAt *time.Time           `json:"last_message_at"`
	LastMessage   *LastMessageResource `json:"last_message"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

func newChatResource(view domain.ChatView) ChatResource {
	resource := ChatResource{
		ID:            view.ID,
		InstanceID:    view.InstanceID,
		JID:           view.JID,
		Phone:         view.Phone,
		LID:           view.LID,
		Type:          view.Type,
		Name:          view.Name,
		LastMessageAt: view.LastMessageAt,
		CreatedAt:     view.CreatedAt,
		UpdatedAt:     view.UpdatedAt,
	}
	if view.LastMessage != nil {
		resource.LastMessage = &LastMessageResource{
			ID:        view.LastMessage.ID,
			Type:      view.LastMessage.Type,
			Text:      view.LastMessage.Text,
			Direction: view.LastMessage.Direction,
			Timestamp: view.LastMessage.Timestamp,
		}
	}
	return resource
}
