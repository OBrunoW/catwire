package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
)

type PresenceSender interface {
	Send(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, state string) error
}

type PresenceHandler struct {
	presence PresenceSender
}

func NewPresenceHandler(presence PresenceSender) *PresenceHandler {
	return &PresenceHandler{presence: presence}
}

type presenceBody struct {
	State string `json:"state"`
}

func (h *PresenceHandler) Send(w http.ResponseWriter, r *http.Request) {
	instanceID, chatID, err := instanceAndChat(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body presenceBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	if err := h.presence.Send(r.Context(), clientID(r), instanceID, chatID, body.State); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.NoContent(w)
}
