package webhook

import (
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type Resource struct {
	ID          uuid.UUID   `json:"id"`
	URL         string      `json:"url"`
	Description *string     `json:"description"`
	Events      []string    `json:"events"`
	InstanceIDs []uuid.UUID `json:"instance_ids"`
	Active      bool        `json:"active"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type CreatedResource struct {
	Resource
	Secret string `json:"secret"`
}

type SecretResource struct {
	ID     uuid.UUID `json:"id"`
	Secret string    `json:"secret"`
}

func newResource(item domain.Webhook) Resource {
	events := item.Events
	if events == nil {
		events = []string{}
	}
	instanceIDs := item.InstanceIDs
	if instanceIDs == nil {
		instanceIDs = []uuid.UUID{}
	}
	return Resource{
		ID:          item.ID,
		URL:         item.URL,
		Description: item.Description,
		Events:      events,
		InstanceIDs: instanceIDs,
		Active:      item.Active,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}
