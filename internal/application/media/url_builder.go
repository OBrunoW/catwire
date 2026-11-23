package media

import (
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

type URLBuilder struct {
	signer  *Signer
	baseURL string
	ttl     time.Duration
}

func NewURLBuilder(signer *Signer, baseURL string, ttl time.Duration) *URLBuilder {
	if ttl <= 0 {
		ttl = defaultURLTTL
	}
	return &URLBuilder{signer: signer, baseURL: baseURL, ttl: ttl}
}

func (b *URLBuilder) Resource(item domain.Media) domain.MediaResource {
	resource := domain.MediaResource{
		ID:              item.ID,
		Kind:            item.Kind,
		Status:          item.Status,
		MimeType:        item.MimeType,
		FileName:        item.FileName,
		SizeBytes:       item.SizeBytes,
		SHA256:          item.SHA256,
		Width:           item.Width,
		Height:          item.Height,
		DurationSeconds: item.DurationSeconds,
		IsVoiceNote:     item.IsVoiceNote,
	}
	if b == nil || b.signer == nil || b.baseURL == "" || item.Status != domain.MediaStatusStored {
		return resource
	}
	downloadURL, expiresAt := b.signer.SignedURL(b.baseURL, item.ID, b.ttl)
	resource.DownloadURL = &downloadURL
	resource.DownloadURLExpiresAt = &expiresAt
	return resource
}
