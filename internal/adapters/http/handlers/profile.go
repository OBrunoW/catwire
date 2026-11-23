package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/operations"
)

type ProfileService interface {
	Get(ctx context.Context, clientID string, instanceID uuid.UUID) (operations.Profile, error)
	Update(ctx context.Context, clientID string, instanceID uuid.UUID, update operations.ProfileUpdate) (operations.Profile, error)
}

type ProfileHandler struct {
	profiles ProfileService
}

func NewProfileHandler(profiles ProfileService) *ProfileHandler {
	return &ProfileHandler{profiles: profiles}
}

type profileResource struct {
	PushName      string  `json:"push_name"`
	StatusMessage string  `json:"status_message"`
	PictureURL    *string `json:"picture_url"`
	BusinessName  *string `json:"business_name"`
}

type profileBody struct {
	PushName      *string `json:"push_name"`
	StatusMessage *string `json:"status_message"`
}

func newProfileResource(profile operations.Profile) profileResource {
	return profileResource{
		PushName:      profile.PushName,
		StatusMessage: profile.StatusMessage,
		PictureURL:    profile.PictureURL,
		BusinessName:  profile.BusinessName,
	}
}

func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	profile, err := h.profiles.Get(r.Context(), clientID(r), instanceID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, newProfileResource(profile))
}

func (h *ProfileHandler) Update(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body profileBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	update := operations.ProfileUpdate{PushName: body.PushName, StatusMessage: body.StatusMessage}
	profile, err := h.profiles.Update(r.Context(), clientID(r), instanceID, update)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, newProfileResource(profile))
}
