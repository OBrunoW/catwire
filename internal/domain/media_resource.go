package domain

import (
	"time"

	"github.com/google/uuid"
)

type MediaResource struct {
	ID                   uuid.UUID  `json:"id"`
	Kind                 string     `json:"kind"`
	Status               string     `json:"status"`
	MimeType             *string    `json:"mime_type"`
	FileName             *string    `json:"file_name"`
	SizeBytes            *int64     `json:"size_bytes"`
	SHA256               *string    `json:"sha256"`
	Width                *int       `json:"width"`
	Height               *int       `json:"height"`
	DurationSeconds      *int       `json:"duration_seconds"`
	IsVoiceNote          bool       `json:"is_voice_note"`
	DownloadURL          *string    `json:"download_url"`
	DownloadURLExpiresAt *time.Time `json:"download_url_expires_at"`
}

type ErrorResource struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
