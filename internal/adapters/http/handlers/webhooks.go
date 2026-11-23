package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/domain"
)

type WebhookService interface {
	Create(ctx context.Context, clientID string, input webhook.CreateInput) (webhook.CreatedResource, error)
	Get(ctx context.Context, clientID string, id uuid.UUID) (webhook.Resource, error)
	List(ctx context.Context, clientID string, active *bool, params apihttp.PageParams) (apihttp.PageResult[webhook.Resource], error)
	Update(ctx context.Context, clientID string, id uuid.UUID, update domain.WebhookUpdate) (webhook.Resource, error)
	Delete(ctx context.Context, clientID string, id uuid.UUID) error
	RotateSecret(ctx context.Context, clientID string, id uuid.UUID) (webhook.SecretResource, error)
	SendTest(ctx context.Context, clientID string, id uuid.UUID) (uuid.UUID, error)
	ListDeliveries(ctx context.Context, clientID string, webhookID uuid.UUID, filter domain.DeliveryFilter, params apihttp.PageParams) (apihttp.PageResult[domain.WebhookDelivery], error)
	RetryDelivery(ctx context.Context, clientID string, webhookID, id uuid.UUID) (*domain.WebhookDelivery, error)
}

type WebhooksHandler struct {
	webhooks WebhookService
}

func NewWebhooksHandler(webhooks WebhookService) *WebhooksHandler {
	return &WebhooksHandler{webhooks: webhooks}
}

type createWebhookBody struct {
	URL         string      `json:"url"`
	Description *string     `json:"description"`
	Events      []string    `json:"events"`
	InstanceIDs []uuid.UUID `json:"instance_ids"`
	Active      *bool       `json:"active"`
}

type updateWebhookBody struct {
	URL         *string      `json:"url"`
	Description *string      `json:"description"`
	Events      *[]string    `json:"events"`
	InstanceIDs *[]uuid.UUID `json:"instance_ids"`
	Active      *bool        `json:"active"`
}

type testDeliveryResource struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

type deliveryResource struct {
	ID             uuid.UUID  `json:"id"`
	WebhookID      uuid.UUID  `json:"webhook_id"`
	EventID        uuid.UUID  `json:"event_id"`
	EventType      string     `json:"event_type"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	LastStatusCode *int       `json:"last_status_code"`
	LastError      *string    `json:"last_error"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	DeliveredAt    *time.Time `json:"delivered_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func newDeliveryResource(delivery domain.WebhookDelivery) deliveryResource {
	return deliveryResource{
		ID:             delivery.ID,
		WebhookID:      delivery.WebhookID,
		EventID:        delivery.EventID,
		EventType:      delivery.EventType,
		Status:         delivery.Status,
		Attempts:       delivery.Attempts,
		LastStatusCode: delivery.LastStatusCode,
		LastError:      delivery.LastError,
		NextAttemptAt:  delivery.NextAttemptAt,
		DeliveredAt:    delivery.DeliveredAt,
		CreatedAt:      delivery.CreatedAt,
		UpdatedAt:      delivery.UpdatedAt,
	}
}

func (h *WebhooksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body createWebhookBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	created, err := h.webhooks.Create(r.Context(), clientID(r), webhook.CreateInput{
		URL:         body.URL,
		Description: body.Description,
		Events:      body.Events,
		InstanceIDs: body.InstanceIDs,
		Active:      body.Active,
	})
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Created(w, created)
}

func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	active, err := queryBool(r, "active")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	page, err := h.webhooks.List(r.Context(), clientID(r), active, apihttp.ParsePageParams(r))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, page)
}

func (h *WebhooksHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	found, err := h.webhooks.Get(r.Context(), clientID(r), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, found)
}

func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body updateWebhookBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	updated, err := h.webhooks.Update(r.Context(), clientID(r), id, domain.WebhookUpdate(body))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, updated)
}

func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	if err := h.webhooks.Delete(r.Context(), clientID(r), id); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.NoContent(w)
}

func (h *WebhooksHandler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	rotated, err := h.webhooks.RotateSecret(r.Context(), clientID(r), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, rotated)
}

func (h *WebhooksHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	deliveryID, err := h.webhooks.SendTest(r.Context(), clientID(r), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Accepted(w, testDeliveryResource{DeliveryID: deliveryID})
}

func (h *WebhooksHandler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	query := r.URL.Query()
	filter := domain.DeliveryFilter{Status: query.Get("status"), EventType: query.Get("event_type")}
	page, err := h.webhooks.ListDeliveries(r.Context(), clientID(r), id, filter, apihttp.ParsePageParams(r))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, mapPage(page, newDeliveryResource))
}

func (h *WebhooksHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	webhookID, err := pathUUID(r, "webhook_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	deliveryID, err := pathUUID(r, "delivery_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	delivery, err := h.webhooks.RetryDelivery(r.Context(), clientID(r), webhookID, deliveryID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Accepted(w, newDeliveryResource(*delivery))
}
