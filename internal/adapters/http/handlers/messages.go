package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/domain"
)

const idempotencyKeyHeader = "Idempotency-Key"

type MessageSender interface {
	Send(ctx context.Context, clientID string, instanceID uuid.UUID, idempotencyKey string, request outbound.SendRequest) (outbound.SendResult, error)
}

type MessageQueries interface {
	ListMessages(ctx context.Context, clientID string, instanceID uuid.UUID, filter domain.MessageFilter, params apihttp.PageParams) (apihttp.PageResult[ingestion.MessageResource], error)
	GetMessage(ctx context.Context, clientID string, instanceID, id uuid.UUID) (ingestion.MessageResource, error)
}

type MessagesHandler struct {
	sender    MessageSender
	queries   MessageQueries
	bodyLimit int64
}

func NewMessagesHandler(sender MessageSender, queries MessageQueries, bodyLimit int64) *MessagesHandler {
	return &MessagesHandler{sender: sender, queries: queries, bodyLimit: bodyLimit}
}

func (h *MessagesHandler) Send(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var request outbound.SendRequest
	options := bodyOptions{limit: h.bodyLimit, tooLarge: domain.MediaTooLarge()}
	if err := decodeBody(w, r, &request, options); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	result, err := h.sender.Send(r.Context(), clientID(r), instanceID, r.Header.Get(idempotencyKeyHeader), request)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	if result.Replayed {
		apihttp.OK(w, result.Resource())
		return
	}
	apihttp.Accepted(w, result.Resource())
}

func (h *MessagesHandler) List(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	filter, err := messageFilter(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	page, err := h.queries.ListMessages(r.Context(), clientID(r), instanceID, filter, apihttp.ParsePageParams(r))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, page)
}

func (h *MessagesHandler) Get(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	messageID, err := pathUUID(r, "message_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	message, err := h.queries.GetMessage(r.Context(), clientID(r), instanceID, messageID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, message)
}

func messageFilter(r *http.Request) (domain.MessageFilter, error) {
	chatID, err := queryUUID(r, "chat_id")
	if err != nil {
		return domain.MessageFilter{}, err
	}
	since, err := queryTime(r, "since")
	if err != nil {
		return domain.MessageFilter{}, err
	}
	until, err := queryTime(r, "until")
	if err != nil {
		return domain.MessageFilter{}, err
	}
	query := r.URL.Query()
	return domain.MessageFilter{
		ChatID:      chatID,
		WAMessageID: query.Get("wa_message_id"),
		Direction:   query.Get("direction"),
		Type:        query.Get("type"),
		Source:      query.Get("source"),
		Since:       since,
		Until:       until,
		Search:      query.Get("search"),
	}, nil
}
