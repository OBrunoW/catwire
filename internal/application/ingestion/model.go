package ingestion

import (
	"encoding/json"
	"time"
)

const (
	SourceLive    = "live"
	SourceHistory = "history"

	OriginContact = "contact"
	OriginDevice  = "device"

	MessageTypeText          = "text"
	MessageTypeImage         = "image"
	MessageTypeAudio         = "audio"
	MessageTypeVideo         = "video"
	MessageTypeDocument      = "document"
	MessageTypeSticker       = "sticker"
	MessageTypeLocation      = "location"
	MessageTypeContact       = "contact"
	MessageTypeUndecryptable = "undecryptable"
	MessageTypeUnsupported   = "unsupported"
)

type ChatRef struct {
	JID   string
	Phone *string
	LID   *string
	Group bool
	Name  *string
}

type Sender struct {
	JID      string
	Phone    *string
	LID      *string
	PushName *string
}

type MediaDescriptor struct {
	Kind            string
	MimeType        *string
	FileName        *string
	SizeBytes       *int64
	Width           *int
	Height          *int
	DurationSeconds *int
	IsVoiceNote     bool
	DirectPath      *string
	MediaKey        []byte
	FileSHA256      []byte
	FileEncSHA256   []byte
}

type Content struct {
	Type               string
	Text               *string
	Data               json.RawMessage
	Media              *MediaDescriptor
	ReplyToWAMessageID *string
	IsForwarded        bool
	IsEphemeral        bool
	IsViewOnce         bool
}

type IncomingMessage struct {
	WAMessageID string
	Chat        ChatRef
	Sender      Sender
	FromMe      bool
	Timestamp   time.Time
	Source      string
	Content     Content
}

type EditChange struct {
	Chat              ChatRef
	TargetWAMessageID string
	Content           Content
	At                time.Time
}

type RevocationChange struct {
	Chat              ChatRef
	TargetWAMessageID string
	At                time.Time
}

type ReactionChange struct {
	Chat              ChatRef
	TargetWAMessageID string
	SenderJID         string
	Emoji             string
	At                time.Time
}

type ReceiptChange struct {
	Chat         ChatRef
	WAMessageIDs []string
	Status       string
	At           time.Time
}

type HistoryConversation struct {
	Chat     ChatRef
	Messages []IncomingMessage
}

type HistoryBatch struct {
	SyncType      string
	ChunkOrder    int
	Progress      int
	Conversations []HistoryConversation
}
