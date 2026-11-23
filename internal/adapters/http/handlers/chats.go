package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/operations"
	"github.com/OBrunoW/catwire/internal/application/query"
	"github.com/OBrunoW/catwire/internal/domain"
)

type ChatQueries interface {
	ListChats(ctx context.Context, clientID string, instanceID uuid.UUID, filter domain.ChatFilter, params apihttp.PageParams) (apihttp.PageResult[query.ChatResource], error)
	GetChat(ctx context.Context, clientID string, instanceID, id uuid.UUID) (query.ChatResource, error)
}

type ChatReader interface {
	Mark(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, messageIDs []uuid.UUID) (operations.ReadResult, error)
}

type ChatsHandler struct {
	queries ChatQueries
	reads   ChatReader
}

func NewChatsHandler(queries ChatQueries, reads ChatReader) *ChatsHandler {
	return &ChatsHandler{queries: queries, reads: reads}
}

type markReadBody struct {
	MessageIDs []uuid.UUID `json:"message_ids"`
}

type markReadResource struct {
	ChatID uuid.UUID `json:"chat_id"`
	Marked int       `json:"marked"`
}

func (h *ChatsHandler) List(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	values := r.URL.Query()
	filter := domain.ChatFilter{Search: values.Get("search"), Type: values.Get("type")}
	page, err := h.queries.ListChats(r.Context(), clientID(r), instanceID, filter, apihttp.ParsePageParams(r))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, page)
}

func (h *ChatsHandler) Get(w http.ResponseWriter, r *http.Request) {
	instanceID, chatID, err := instanceAndChat(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	chat, err := h.queries.GetChat(r.Context(), clientID(r), instanceID, chatID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, chat)
}

func (h *ChatsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	instanceID, chatID, err := instanceAndChat(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body markReadBody
	if err := decodeBody(w, r, &body, bodyOptions{optional: true}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	result, err := h.reads.Mark(r.Context(), clientID(r), instanceID, chatID, body.MessageIDs)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, markReadResource{ChatID: result.ChatID, Marked: result.Marked})
}

func instanceAndChat(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	chatID, err := pathUUID(r, "chat_id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return instanceID, chatID, nil
}
