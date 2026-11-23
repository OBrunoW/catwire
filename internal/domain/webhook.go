package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	DeliveryStatusPending   = "pending"
	DeliveryStatusFailed    = "failed"
	DeliveryStatusSucceeded = "succeeded"
	DeliveryStatusDead      = "dead"
)

type Webhook struct {
	ID          uuid.UUID
	ClientID    string
	URL         string
	Description *string
	Secret      string
	Events      []string
	InstanceIDs []uuid.UUID
	Active      bool
	DeletedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type WebhookUpdate struct {
	URL         *string
	Description *string
	Events      *[]string
	InstanceIDs *[]uuid.UUID
	Active      *bool
}

type WebhookDelivery struct {
	ID             uuid.UUID
	WebhookID      uuid.UUID
	EventID        uuid.UUID
	EventType      string
	Payload        json.RawMessage
	Status         string
	Attempts       int
	NextAttemptAt  time.Time
	LastStatusCode *int
	LastError      *string
	LastAttemptAt  *time.Time
	DeliveredAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type DueDelivery struct {
	WebhookDelivery
	WebhookURL    string
	WebhookSecret string
}

type DeliveryFilter struct {
	Status    string
	EventType string
}
