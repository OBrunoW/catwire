package media

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	EventTypeMediaStored = "whatsapp.media.stored"
	EventTypeMediaFailed = "whatsapp.media.failed"

	CodeMediaTooLarge       = "MEDIA_TOO_LARGE"
	CodeMediaUnavailable    = "MEDIA_UNAVAILABLE"
	CodeMediaDownloadFailed = "MEDIA_DOWNLOAD_FAILED"
)

type MediaEventData struct {
	MessageID   uuid.UUID            `json:"message_id"`
	WAMessageID string               `json:"wa_message_id"`
	ChatID      uuid.UUID            `json:"chat_id"`
	ChatJID     string               `json:"chat_jid"`
	Media       domain.MediaResource `json:"media"`
}

type MediaFailedData struct {
	MediaEventData
	Error domain.ErrorResource `json:"error"`
}

type owner struct {
	message  domain.Message
	chat     domain.Chat
	instance domain.Instance
}

func newMediaResource(item domain.Media) domain.MediaResource {
	return domain.MediaResource{
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
}

func (o owner) eventData(resource domain.MediaResource) MediaEventData {
	return MediaEventData{
		MessageID:   o.message.ID,
		WAMessageID: o.message.WAMessageID,
		ChatID:      o.chat.ID,
		ChatJID:     o.chat.JID,
		Media:       resource,
	}
}

func (o owner) partitionKey() string {
	return o.instance.ID.String() + ":" + o.chat.JID
}

func newOutboxEvent(o owner, eventType string, data any, occurredAt time.Time) (*domain.OutboxEvent, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	instanceID := o.instance.ID
	return &domain.OutboxEvent{
		EventID:      uuid.New(),
		EventType:    eventType,
		EventVersion: 1,
		ClientID:     o.instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: o.partitionKey(),
		Payload:      payload,
		OccurredAt:   occurredAt,
	}, nil
}

func failureMessage(code string) string {
	switch code {
	case CodeMediaTooLarge:
		return "media exceeds the size limit"
	case CodeMediaUnavailable:
		return "media is no longer available"
	default:
		return "media download failed"
	}
}

func redact(text string, item domain.Media) string {
	for _, secret := range secretsOf(item) {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}

func secretsOf(item domain.Media) []string {
	secrets := []string{}
	if item.WADirectPath != nil && *item.WADirectPath != "" {
		secrets = append(secrets, *item.WADirectPath, url.PathEscape(*item.WADirectPath), url.QueryEscape(*item.WADirectPath))
	}
	for _, raw := range [][]byte{item.WAMediaKey, item.WAFileSHA256, item.WAFileEncSHA256} {
		if len(raw) == 0 {
			continue
		}
		secrets = append(secrets,
			hex.EncodeToString(raw),
			base64.StdEncoding.EncodeToString(raw),
			base64.URLEncoding.EncodeToString(raw),
			base64.RawStdEncoding.EncodeToString(raw),
			base64.RawURLEncoding.EncodeToString(raw),
		)
	}
	return secrets
}
