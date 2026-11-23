package outbound

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	MessageTypeText     = "text"
	MessageTypeImage    = "image"
	MessageTypeAudio    = "audio"
	MessageTypeVideo    = "video"
	MessageTypeDocument = "document"

	MaxTextLength        = 65536
	MaxMetadataBytes     = 4096
	MaxIdempotencyKeyLen = 200
	minPhoneDigits       = 8
	maxPhoneDigits       = 15

	userServer   = "s.whatsapp.net"
	hiddenServer = "lid"
)

type MediaInput struct {
	URL         string     `json:"url"`
	Base64      string     `json:"base64"`
	MediaID     *uuid.UUID `json:"media_id"`
	MimeType    string     `json:"mime_type"`
	FileName    string     `json:"file_name"`
	AsVoiceNote bool       `json:"as_voice_note"`
}

type SendRequest struct {
	To               string          `json:"to"`
	ChatID           *uuid.UUID      `json:"chat_id"`
	Type             string          `json:"type"`
	Text             *string         `json:"text"`
	Media            *MediaInput     `json:"media"`
	ReplyToMessageID *uuid.UUID      `json:"reply_to_message_id"`
	Metadata         json.RawMessage `json:"metadata"`
}

func (r SendRequest) Validate() error {
	if err := r.validateRecipient(); err != nil {
		return err
	}
	if err := r.validateContent(); err != nil {
		return err
	}
	return validateMetadata(r.Metadata)
}

func (r SendRequest) validateRecipient() error {
	if (r.To == "") == (r.ChatID == nil) {
		return domain.BadRequest("exactly one of to and chat_id is required")
	}
	if r.To == "" {
		return nil
	}
	if _, _, err := parseRecipient(r.To); err != nil {
		return err
	}
	return nil
}

func (r SendRequest) validateContent() error {
	switch r.Type {
	case MessageTypeText:
		return r.validateText()
	case MessageTypeAudio:
		return r.validateAudio()
	case MessageTypeImage, MessageTypeVideo, MessageTypeDocument:
		return r.validateCaptioned()
	}
	return domain.BadRequest("type must be one of text, image, audio, video, document")
}

func (r SendRequest) validateText() error {
	if r.Media != nil {
		return domain.BadRequest("media is not allowed for type text")
	}
	if r.Text == nil || *r.Text == "" {
		return domain.BadRequest("text is required for type text")
	}
	return validateTextLength(*r.Text)
}

func (r SendRequest) validateAudio() error {
	if r.Text != nil && *r.Text != "" {
		return domain.BadRequest("text is not allowed for type audio")
	}
	return validateMedia(r.Media, true)
}

func (r SendRequest) validateCaptioned() error {
	if r.Text != nil {
		if err := validateTextLength(*r.Text); err != nil {
			return err
		}
	}
	return validateMedia(r.Media, false)
}

func validateTextLength(text string) error {
	if utf8.RuneCountInString(text) > MaxTextLength {
		return domain.BadRequest("text exceeds 65536 characters")
	}
	return nil
}

func validateMedia(media *MediaInput, voiceNoteAllowed bool) error {
	if media == nil {
		return domain.BadRequest("media is required for this type")
	}
	if media.sourceCount() != 1 {
		return domain.BadRequest("media requires exactly one of url, base64, media_id")
	}
	if media.Base64 != "" && media.MimeType == "" {
		return domain.BadRequest("media.mime_type is required with base64")
	}
	if media.AsVoiceNote && !voiceNoteAllowed {
		return domain.BadRequest("media.as_voice_note is only allowed for type audio")
	}
	if media.URL != "" {
		return validateMediaURL(media.URL)
	}
	return nil
}

func (m MediaInput) sourceCount() int {
	count := 0
	if m.URL != "" {
		count++
	}
	if m.Base64 != "" {
		count++
	}
	if m.MediaID != nil {
		count++
	}
	return count
}

func validateMediaURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return domain.BadRequest("media.url must be an http or https url")
	}
	return nil
}

func validateMetadata(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) > MaxMetadataBytes {
		return domain.BadRequest("metadata exceeds 4096 bytes")
	}
	var object map[string]any
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return domain.BadRequest("metadata must be a json object")
	}
	return nil
}

type recipient struct {
	phone string
	jid   string
}

func parseRecipient(raw string) (recipient, bool, error) {
	user, server, hasServer := strings.Cut(raw, "@")
	if !hasServer {
		if !validPhone(raw) {
			return recipient{}, false, domain.BadRequest("to must be digits with country code or a user jid")
		}
		return recipient{phone: raw}, true, nil
	}
	switch server {
	case userServer:
		if !validPhone(user) {
			return recipient{}, false, domain.BadRequest("to must be digits with country code or a user jid")
		}
		return recipient{phone: user}, true, nil
	case hiddenServer:
		if user == "" || strings.ContainsAny(user, ":@") {
			return recipient{}, false, domain.BadRequest("to must be digits with country code or a user jid")
		}
		return recipient{jid: raw}, false, nil
	}
	return recipient{}, false, domain.BadRequest("to must be digits with country code or a user jid")
}

func validPhone(value string) bool {
	if len(value) < minPhoneDigits || len(value) > maxPhoneDigits {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (r SendRequest) Hash() (string, error) {
	canonical, err := json.Marshal(canonicalBody{
		To:               r.To,
		ChatID:           r.ChatID,
		Type:             r.Type,
		Text:             r.Text,
		Media:            r.Media,
		ReplyToMessageID: r.ReplyToMessageID,
	}.withMetadata(r.Metadata))
	if err != nil {
		return "", fmt.Errorf("outbound: encode canonical body: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

type canonicalBody struct {
	To               string      `json:"to"`
	ChatID           *uuid.UUID  `json:"chat_id"`
	Type             string      `json:"type"`
	Text             *string     `json:"text"`
	Media            *MediaInput `json:"media"`
	ReplyToMessageID *uuid.UUID  `json:"reply_to_message_id"`
	Metadata         any         `json:"metadata"`
}

func (b canonicalBody) withMetadata(raw json.RawMessage) canonicalBody {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return b
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err == nil {
		b.Metadata = decoded
	}
	return b
}
