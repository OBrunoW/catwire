package outbound

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var ErrInstanceDisconnected = errors.New("outbound: instance is not connected")

type PermanentError struct {
	Code    string
	Message string
}

func (e *PermanentError) Error() string { return e.Code + ": " + e.Message }

type Registration struct {
	Exists bool
	JID    string
}

type ReplyReference struct {
	WAMessageID string
	SenderJID   string
	Text        *string
}

type MediaPayload struct {
	MimeType  string
	FileName  string
	VoiceNote bool
	Size      int64
	Body      io.Reader
}

type SendCommand struct {
	InstanceID  uuid.UUID
	ChatJID     string
	WAMessageID string
	Type        string
	Text        *string
	Media       *MediaPayload
	Reply       *ReplyReference
}

type SendReceipt struct {
	Timestamp time.Time
}

type Gateway interface {
	GenerateMessageID(instanceID uuid.UUID) (string, error)
	IsOnWhatsApp(ctx context.Context, instanceID uuid.UUID, phone string) (Registration, error)
	Send(ctx context.Context, command SendCommand) (SendReceipt, error)
}

type FetchedFile struct {
	Body        io.ReadCloser
	ContentType string
	FileName    string
}

type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (FetchedFile, error)
}

type InstanceStore interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
	FindUnscoped(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
}

type ChatStore interface {
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error)
	FindByJID(ctx context.Context, instanceID uuid.UUID, jid string) (*domain.Chat, error)
	FindByLID(ctx context.Context, instanceID uuid.UUID, lid string) (*domain.Chat, error)
	Upsert(ctx context.Context, chat *domain.Chat) error
	TouchLastMessage(ctx context.Context, instanceID, id, messageID uuid.UUID, at time.Time) error
}

type MessageStore interface {
	Insert(ctx context.Context, message *domain.Message) (bool, error)
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.Message, error)
	FindByChatAndWAMessageID(ctx context.Context, instanceID, chatID uuid.UUID, waMessageID string) (*domain.Message, error)
	FindByIdempotencyKey(ctx context.Context, instanceID uuid.UUID, key string) (*domain.Message, error)
	AdvanceStatus(ctx context.Context, instanceID, id uuid.UUID, status string, at time.Time) (domain.StatusAdvance, bool, error)
	MarkFailed(ctx context.Context, instanceID, id uuid.UUID, code, message string) (bool, error)
	ScheduleSendRetry(ctx context.Context, instanceID, id uuid.UUID, nextAttemptAt time.Time) error
	ListPendingForSend(ctx context.Context, instanceID uuid.UUID, limit int) ([]domain.Message, error)
	ListInstancesWithPendingSend(ctx context.Context) ([]uuid.UUID, error)
}

type MediaStore interface {
	Insert(ctx context.Context, media *domain.Media) error
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Media, error)
	FindByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) ([]domain.Media, error)
	Attach(ctx context.Context, instanceID, id, messageID uuid.UUID) error
}

type OutboxWriter interface {
	Insert(ctx context.Context, event *domain.OutboxEvent) error
}

type Unit struct {
	Instances InstanceStore
	Chats     ChatStore
	Messages  MessageStore
	Media     MediaStore
	Outbox    OutboxWriter
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}
