package media

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var (
	ErrObjectNotFound = errors.New("media: stored object not found")
	ErrMediaGone      = errors.New("media: source no longer available")

	ErrInstanceDisconnected = errors.New("media: instance is not connected")
)

type permanentError struct {
	code string
	err  error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func Permanent(code string, err error) error {
	return &permanentError{code: code, err: err}
}

type ObjectInfo struct {
	Size    int64
	ModTime time.Time
}

type Storage interface {
	Put(ctx context.Context, key string, reader io.Reader, size int64, mimeType string) error
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
}

type DownloadRequest struct {
	InstanceID    uuid.UUID
	Kind          string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
}

type DownloadTarget interface {
	io.Reader
	io.Writer
	io.Seeker
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
	Stat() (os.FileInfo, error)
}

type Downloader interface {
	Download(ctx context.Context, request DownloadRequest, destination DownloadTarget) error
}

type RetryTarget struct {
	WAMessageID string
	ChatJID     string
	SenderJID   string
	FromMe      bool
	MediaKey    []byte
}

type RetryRequester interface {
	RequestRetry(ctx context.Context, instanceID uuid.UUID, target RetryTarget) error
}

type Converter interface {
	ToOggOpus(ctx context.Context, inputPath, outputPath string) error
}

type MediaStore interface {
	ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]domain.Media, error)
	MarkStored(ctx context.Context, id uuid.UUID, stored domain.StoredMedia) error
	Reschedule(ctx context.Context, id uuid.UUID, nextAttemptAt time.Time) error
	RecordDownloadFailure(ctx context.Context, id uuid.UUID, message string, nextAttemptAt *time.Time) error
	ApplyRetryPath(ctx context.Context, id uuid.UUID, directPath string) error
	FindByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) ([]domain.Media, error)
	ListExpiredUnattached(ctx context.Context, limit int) ([]domain.Media, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type MediaFinder interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Media, error)
	FindByIDUnscoped(ctx context.Context, id uuid.UUID) (*domain.Media, error)
}

type MessageStore interface {
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.Message, error)
	FindByWAMessageID(ctx context.Context, instanceID uuid.UUID, waMessageID string) (*domain.Message, error)
}

type ChatReader interface {
	FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error)
}

type InstanceReader interface {
	FindUnscoped(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
}

type OutboxWriter interface {
	Insert(ctx context.Context, event *domain.OutboxEvent) error
}

type Unit struct {
	Media     MediaStore
	Messages  MessageStore
	Chats     ChatReader
	Instances InstanceReader
	Outbox    OutboxWriter
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}
