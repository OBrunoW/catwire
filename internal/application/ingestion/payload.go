package ingestion

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	EventTypeMessageReceived      = "whatsapp.message.received"
	EventTypeMessageSent          = "whatsapp.message.sent"
	EventTypeMessageStatusUpdated = "whatsapp.message.status_updated"
	EventTypeMessageUpdated       = "whatsapp.message.updated"
	EventTypeHistorySynced        = "whatsapp.history.synced"

	ChangeEdited   = "edited"
	ChangeRevoked  = "revoked"
	ChangeReaction = "reaction"
)

type SenderResource struct {
	JID      string  `json:"jid"`
	Phone    *string `json:"phone"`
	LID      *string `json:"lid"`
	PushName *string `json:"push_name"`
}

type ReplyToResource struct {
	WAMessageID string     `json:"wa_message_id"`
	MessageID   *uuid.UUID `json:"message_id"`
}

type MessageResource struct {
	ID          uuid.UUID             `json:"id"`
	InstanceID  uuid.UUID             `json:"instance_id"`
	ChatID      uuid.UUID             `json:"chat_id"`
	ChatJID     string                `json:"chat_jid"`
	WAMessageID string                `json:"wa_message_id"`
	Direction   string                `json:"direction"`
	Origin      string                `json:"origin"`
	Source      string                `json:"source"`
	Sender      SenderResource        `json:"sender"`
	Type        string                `json:"type"`
	Text        *string               `json:"text"`
	Media       *domain.MediaResource `json:"media"`
	Location    json.RawMessage       `json:"location"`
	Contacts    json.RawMessage       `json:"contacts"`
	ReplyTo     *ReplyToResource      `json:"reply_to"`
	IsForwarded bool                  `json:"is_forwarded"`
	IsEphemeral bool                  `json:"is_ephemeral"`
	IsViewOnce  bool                  `json:"is_view_once"`
	Status      string                `json:"status"`
	Error       *domain.ErrorResource `json:"error"`
	Reactions   json.RawMessage       `json:"reactions"`
	Metadata    json.RawMessage       `json:"metadata"`
	Timestamp   time.Time             `json:"timestamp"`
	SentAt      *time.Time            `json:"sent_at"`
	DeliveredAt *time.Time            `json:"delivered_at"`
	ReadAt      *time.Time            `json:"read_at"`
	PlayedAt    *time.Time            `json:"played_at"`
	EditedAt    *time.Time            `json:"edited_at"`
	RevokedAt   *time.Time            `json:"revoked_at"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

type MessageEventData struct {
	Message MessageResource `json:"message"`
}

type StatusUpdatedData struct {
	MessageID      uuid.UUID       `json:"message_id"`
	WAMessageID    string          `json:"wa_message_id"`
	ChatID         uuid.UUID       `json:"chat_id"`
	ChatJID        string          `json:"chat_jid"`
	Direction      string          `json:"direction"`
	Status         string          `json:"status"`
	PreviousStatus string          `json:"previous_status"`
	StatusAt       time.Time       `json:"status_at"`
	Metadata       json.RawMessage `json:"metadata"`
}

type ReactionResource struct {
	SenderJID string    `json:"sender_jid"`
	Emoji     string    `json:"emoji"`
	ReactedAt time.Time `json:"reacted_at"`
	Removed   bool      `json:"removed"`
}

type MessageUpdatedData struct {
	Change   string            `json:"change"`
	Message  MessageResource   `json:"message"`
	Reaction *ReactionResource `json:"reaction"`
}

type HistorySyncedData struct {
	SyncType         string `json:"sync_type"`
	ChunkOrder       int    `json:"chunk_order"`
	Progress         int    `json:"progress"`
	Chats            int    `json:"chats"`
	Messages         int    `json:"messages"`
	InsertedMessages int    `json:"inserted_messages"`
}

func NewMessageResource(
	message domain.Message,
	chatJID string,
	media *domain.Media,
	replyTarget *uuid.UUID,
) MessageResource {
	resource := MessageResource{
		ID:          message.ID,
		InstanceID:  message.InstanceID,
		ChatID:      message.ChatID,
		ChatJID:     chatJID,
		WAMessageID: message.WAMessageID,
		Direction:   message.Direction,
		Origin:      message.Origin,
		Source:      message.Source,
		Sender: SenderResource{
			JID:      message.SenderJID,
			Phone:    message.SenderPhone,
			LID:      message.SenderLID,
			PushName: message.SenderPushName,
		},
		Type:        message.Type,
		Text:        message.Text,
		Media:       mediaResource(media),
		IsForwarded: message.IsForwarded,
		IsEphemeral: message.IsEphemeral,
		IsViewOnce:  message.IsViewOnce,
		Status:      message.Status,
		Error:       errorResource(message),
		Reactions:   rawOr(message.Reactions, "[]"),
		Metadata:    rawOr(message.Metadata, "{}"),
		Timestamp:   message.Timestamp,
		SentAt:      message.SentAt,
		DeliveredAt: message.DeliveredAt,
		ReadAt:      message.ReadAt,
		PlayedAt:    message.PlayedAt,
		EditedAt:    message.EditedAt,
		RevokedAt:   message.RevokedAt,
		CreatedAt:   message.CreatedAt,
		UpdatedAt:   message.UpdatedAt,
	}
	if message.ReplyToWAMessageID != nil {
		resource.ReplyTo = &ReplyToResource{WAMessageID: *message.ReplyToWAMessageID, MessageID: replyTarget}
	}
	switch message.Type {
	case MessageTypeLocation:
		resource.Location = rawOr(message.Content, "null")
	case MessageTypeContact:
		resource.Contacts = rawOr(message.Content, "null")
	}
	return resource
}

func mediaResource(media *domain.Media) *domain.MediaResource {
	if media == nil {
		return nil
	}
	return &domain.MediaResource{
		ID:              media.ID,
		Kind:            media.Kind,
		Status:          media.Status,
		MimeType:        media.MimeType,
		FileName:        media.FileName,
		SizeBytes:       media.SizeBytes,
		SHA256:          media.SHA256,
		Width:           media.Width,
		Height:          media.Height,
		DurationSeconds: media.DurationSeconds,
		IsVoiceNote:     media.IsVoiceNote,
	}
}

func errorResource(message domain.Message) *domain.ErrorResource {
	if message.ErrorCode == nil {
		return nil
	}
	resource := &domain.ErrorResource{Code: *message.ErrorCode}
	if message.ErrorMessage != nil {
		resource.Message = *message.ErrorMessage
	}
	return resource
}

func rawOr(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}
