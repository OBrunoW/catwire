package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/operations"
)

type PrivacyService interface {
	Get(ctx context.Context, clientID string, instanceID uuid.UUID) (operations.PrivacySettings, error)
	Update(ctx context.Context, clientID string, instanceID uuid.UUID, update operations.PrivacyUpdate) (operations.PrivacySettings, error)
}

type PrivacyHandler struct {
	privacy PrivacyService
}

func NewPrivacyHandler(privacy PrivacyService) *PrivacyHandler {
	return &PrivacyHandler{privacy: privacy}
}

type privacyResource struct {
	GroupAdd     string `json:"group_add"`
	LastSeen     string `json:"last_seen"`
	Status       string `json:"status"`
	Profile      string `json:"profile"`
	ReadReceipts string `json:"read_receipts"`
	Online       string `json:"online"`
	CallAdd      string `json:"call_add"`
}

type privacyBody struct {
	GroupAdd     *string `json:"group_add"`
	LastSeen     *string `json:"last_seen"`
	Status       *string `json:"status"`
	Profile      *string `json:"profile"`
	ReadReceipts *string `json:"read_receipts"`
	Online       *string `json:"online"`
	CallAdd      *string `json:"call_add"`
}

func newPrivacyResource(settings operations.PrivacySettings) privacyResource {
	return privacyResource{
		GroupAdd:     settings.GroupAdd,
		LastSeen:     settings.LastSeen,
		Status:       settings.Status,
		Profile:      settings.Profile,
		ReadReceipts: settings.ReadReceipts,
		Online:       settings.Online,
		CallAdd:      settings.CallAdd,
	}
}

func (h *PrivacyHandler) Get(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	settings, err := h.privacy.Get(r.Context(), clientID(r), instanceID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, newPrivacyResource(settings))
}

func (h *PrivacyHandler) Update(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body privacyBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	update := operations.PrivacyUpdate(body)
	settings, err := h.privacy.Update(r.Context(), clientID(r), instanceID, update)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, newPrivacyResource(settings))
}
