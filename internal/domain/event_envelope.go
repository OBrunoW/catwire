package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const EventTypeWebhookTest = "webhook.test"

var EventCatalog = []string{
	"whatsapp.instance.status_changed",
	"whatsapp.message.received",
	"whatsapp.message.sent",
	"whatsapp.message.failed",
	"whatsapp.message.status_updated",
	"whatsapp.message.updated",
	"whatsapp.media.stored",
	"whatsapp.media.failed",
	"whatsapp.history.synced",
}

func IsCatalogEventType(eventType string) bool {
	for _, known := range EventCatalog {
		if known == eventType {
			return true
		}
	}
	return false
}

type EnvelopeInstance struct {
	ID         uuid.UUID `json:"id"`
	ExternalID *string   `json:"external_id"`
	Phone      *string   `json:"phone"`
}

type EventEnvelope struct {
	EventID      uuid.UUID         `json:"event_id"`
	EventType    string            `json:"event_type"`
	EventVersion int               `json:"event_version"`
	Sequence     int64             `json:"sequence"`
	OccurredAt   time.Time         `json:"occurred_at"`
	Instance     *EnvelopeInstance `json:"instance"`
	Data         json.RawMessage   `json:"data"`
}
