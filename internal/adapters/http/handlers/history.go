package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/operations"
)

type HistoryRequester interface {
	Request(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, count int) (operations.HistoryResult, error)
}

type HistoryHandler struct {
	history HistoryRequester
}

func NewHistoryHandler(history HistoryRequester) *HistoryHandler {
	return &HistoryHandler{history: history}
}

type historyBody struct {
	Count int `json:"count"`
}

type historyResource struct {
	RequestID string    `json:"request_id"`
	ChatID    uuid.UUID `json:"chat_id"`
}

func (h *HistoryHandler) Request(w http.ResponseWriter, r *http.Request) {
	instanceID, chatID, err := instanceAndChat(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body historyBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	result, err := h.history.Request(r.Context(), clientID(r), instanceID, chatID, body.Count)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Accepted(w, historyResource{RequestID: result.RequestID, ChatID: result.ChatID})
}
