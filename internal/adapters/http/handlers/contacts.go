package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/contacts"
)

type ContactsService interface {
	Check(ctx context.Context, clientID string, instanceID uuid.UUID, phones []string) ([]contacts.CheckResult, error)
}

type ContactsHandler struct {
	contacts ContactsService
}

func NewContactsHandler(contacts ContactsService) *ContactsHandler {
	return &ContactsHandler{contacts: contacts}
}

type checkContactsBody struct {
	Phones []string `json:"phones"`
}

func (h *ContactsHandler) Check(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body checkContactsBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	results, err := h.contacts.Check(r.Context(), clientID(r), instanceID, body.Phones)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, results)
}
