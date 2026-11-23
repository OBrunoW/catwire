package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type OutboxEvent struct {
	Seq              int64
	EventID          uuid.UUID
	EventType        string
	EventVersion     int16
	ClientID         string
	InstanceID       *uuid.UUID
	PartitionKey     string
	Payload          json.RawMessage
	OccurredAt       time.Time
	DispatchedAt     *time.Time
	KafkaPublishedAt *time.Time
	Attempts         int
	NextAttemptAt    time.Time
	LastError        *string
	CreatedAt        time.Time
}
