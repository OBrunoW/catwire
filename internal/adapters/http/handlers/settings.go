package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/instance"
)

type SettingsService interface {
	GetSettings(ctx context.Context, clientID string, instanceID uuid.UUID) (instance.SettingsResource, error)
	UpdateSettings(ctx context.Context, clientID string, instanceID uuid.UUID, patch instance.SettingsPatch) (instance.SettingsResource, error)
}

type SettingsHandler struct {
	settings SettingsService
}

func NewSettingsHandler(settings SettingsService) *SettingsHandler {
	return &SettingsHandler{settings: settings}
}

func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	settings, err := h.settings.GetSettings(r.Context(), clientID(r), instanceID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, settings)
}

func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var patch instance.SettingsPatch
	if err := decodeBody(w, r, &patch, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	settings, err := h.settings.UpdateSettings(r.Context(), clientID(r), instanceID, patch)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, settings)
}
