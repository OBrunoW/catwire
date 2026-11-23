package instance

import (
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type SettingsResource struct {
	SyncHistory   bool `json:"sync_history"`
	IgnoreGroups  bool `json:"ignore_groups"`
	AutoMarkRead  bool `json:"auto_mark_read"`
	AlwaysOnline  bool `json:"always_online"`
	DownloadMedia bool `json:"download_media"`
}

type Resource struct {
	ID             uuid.UUID        `json:"id"`
	ExternalID     *string          `json:"external_id"`
	Name           string           `json:"name"`
	Status         string           `json:"status"`
	StatusReason   *string          `json:"status_reason"`
	JID            *string          `json:"jid"`
	Phone          *string          `json:"phone"`
	LID            *string          `json:"lid"`
	PushName       *string          `json:"push_name"`
	BusinessName   *string          `json:"business_name"`
	Platform       *string          `json:"platform"`
	ConnectedAt    *time.Time       `json:"connected_at"`
	DisconnectedAt *time.Time       `json:"disconnected_at"`
	Settings       SettingsResource `json:"settings"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

func NewSettingsResource(settings domain.InstanceSettings) SettingsResource {
	return SettingsResource{
		SyncHistory:   settings.SyncHistory,
		IgnoreGroups:  settings.IgnoreGroups,
		AutoMarkRead:  settings.AutoMarkRead,
		AlwaysOnline:  settings.AlwaysOnline,
		DownloadMedia: settings.DownloadMedia,
	}
}

func NewResource(item domain.Instance, settings domain.InstanceSettings) Resource {
	return Resource{
		ID:             item.ID,
		ExternalID:     item.ExternalID,
		Name:           item.Name,
		Status:         item.Status,
		StatusReason:   item.StatusReason,
		JID:            item.JID,
		Phone:          item.Phone,
		LID:            item.LID,
		PushName:       item.PushName,
		BusinessName:   item.BusinessName,
		Platform:       item.Platform,
		ConnectedAt:    item.ConnectedAt,
		DisconnectedAt: item.DisconnectedAt,
		Settings:       NewSettingsResource(settings),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}
