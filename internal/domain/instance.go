package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	InstanceStatusCreated        = "created"
	InstanceStatusConnecting     = "connecting"
	InstanceStatusQRRequired     = "qr_required"
	InstanceStatusConnected      = "connected"
	InstanceStatusDisconnected   = "disconnected"
	InstanceStatusLoggedOut      = "logged_out"
	InstanceStatusBanned         = "banned"
	InstanceStatusClientOutdated = "client_outdated"
)

type Instance struct {
	ID             uuid.UUID
	ClientID       string
	ExternalID     *string
	Name           string
	Status         string
	StatusReason   *string
	AutoConnect    bool
	JID            *string
	Phone          *string
	LID            *string
	PushName       *string
	BusinessName   *string
	Platform       *string
	ConnectedAt    *time.Time
	DisconnectedAt *time.Time
	DeletedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type InstanceSettings struct {
	InstanceID    uuid.UUID
	SyncHistory   bool
	IgnoreGroups  bool
	AutoMarkRead  bool
	AlwaysOnline  bool
	DownloadMedia bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type InstanceIdentity struct {
	JID          *string
	Phone        *string
	LID          *string
	PushName     *string
	BusinessName *string
	Platform     *string
}

type InstanceStatusChange struct {
	Status         string
	Reason         *string
	ConnectedAt    *time.Time
	DisconnectedAt *time.Time
}

type InstanceUpdate struct {
	Name       *string
	ExternalID *string
}

type InstanceFilter struct {
	Search     string
	Status     string
	ExternalID string
}
