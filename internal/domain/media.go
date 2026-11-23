package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	MediaStatusPending = "pending"
	MediaStatusStored  = "stored"
	MediaStatusFailed  = "failed"
	MediaStatusSkipped = "skipped"
)

type Media struct {
	ID               uuid.UUID
	InstanceID       uuid.UUID
	MessageID        *uuid.UUID
	Kind             string
	Status           string
	MimeType         *string
	FileName         *string
	SizeBytes        *int64
	SHA256           *string
	Width            *int
	Height           *int
	DurationSeconds  *int
	IsVoiceNote      bool
	StorageBackend   *string
	StorageKey       *string
	WADirectPath     *string
	WAMediaKey       []byte
	WAFileSHA256     []byte
	WAFileEncSHA256  []byte
	DownloadAttempts int
	NextAttemptAt    *time.Time
	LastError        *string
	StoredAt         *time.Time
	ExpiresAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type StoredMedia struct {
	StorageBackend  string
	StorageKey      string
	MimeType        string
	SizeBytes       int64
	SHA256          string
	Width           *int
	Height          *int
	DurationSeconds *int
}
