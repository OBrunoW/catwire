package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var ErrDeviceNotFound = errors.New("session: paired device not found in the session store")

type EventKind int

const (
	EventConnected EventKind = iota + 1
	EventDisconnected
	EventPaired
	EventLoggedOut
	EventStreamReplaced
	EventTemporaryBan
	EventBanned
	EventClientOutdated
	EventConnectFailure
	EventQRTimeout
	EventQRFailed
)

type Event struct {
	Kind     EventKind
	Reason   string
	Identity domain.InstanceIdentity
}

type QRItemKind int

const (
	QRItemCode QRItemKind = iota + 1
	QRItemSuccess
	QRItemTimeout
	QRItemClientOutdated
	QRItemFailed
)

type QRItem struct {
	Kind    QRItemKind
	Code    string
	Timeout time.Duration
	Reason  string
}

type Client interface {
	Connect() error
	Disconnect()
	Logout(ctx context.Context) error
	DeleteDevice(ctx context.Context) error
	IsConnected() bool
	OpenQRChannel(ctx context.Context) (<-chan QRItem, error)
	PairPhone(ctx context.Context, phone string) (string, error)
	Release()
}

type ClientFactory interface {
	Create(ctx context.Context, instance domain.Instance, emit func(Event)) (Client, error)
}

type InstanceReader interface {
	FindUnscoped(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
	ListReconnectable(ctx context.Context) ([]domain.Instance, error)
}

type InstanceWriter interface {
	UpdateStatus(ctx context.Context, id uuid.UUID, change domain.InstanceStatusChange) (string, error)
	UpdateIdentity(ctx context.Context, id uuid.UUID, identity domain.InstanceIdentity) error
	SetAutoConnect(ctx context.Context, id uuid.UUID, autoConnect bool) error
	SoftDelete(ctx context.Context, clientID string, id uuid.UUID) error
}

type OutboxWriter interface {
	Insert(ctx context.Context, event *domain.OutboxEvent) error
}

type Unit struct {
	Instances InstanceWriter
	Outbox    OutboxWriter
}

type UnitOfWork interface {
	Do(ctx context.Context, run func(unit Unit) error) error
}
