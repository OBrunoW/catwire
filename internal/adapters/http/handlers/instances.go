package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/instance"
	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/domain"
)

const qrRequiredStatus = "qr_required"

type InstanceService interface {
	Create(ctx context.Context, clientID string, input instance.CreateInput) (instance.Resource, error)
	Get(ctx context.Context, clientID string, id uuid.UUID) (instance.Resource, error)
	List(ctx context.Context, clientID string, filter domain.InstanceFilter, params apihttp.PageParams) (apihttp.PageResult[instance.Resource], error)
	Update(ctx context.Context, clientID string, id uuid.UUID, input instance.UpdateInput) (instance.Resource, error)
}

type SessionManager interface {
	Connect(ctx context.Context, id uuid.UUID) (session.ConnectResult, error)
	QR(ctx context.Context, id uuid.UUID) (session.QRCode, error)
	PairPhone(ctx context.Context, id uuid.UUID, phone string) (session.PairCode, error)
	Disconnect(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
	Logout(ctx context.Context, id uuid.UUID) (*domain.Instance, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type InstancesHandler struct {
	instances InstanceService
	sessions  SessionManager
}

func NewInstancesHandler(instances InstanceService, sessions SessionManager) *InstancesHandler {
	return &InstancesHandler{instances: instances, sessions: sessions}
}

type qrResource struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type connectResource struct {
	Instance instance.Resource `json:"instance"`
	QR       *qrResource       `json:"qr"`
}

type qrCodeResource struct {
	Status string     `json:"status"`
	QR     qrResource `json:"qr"`
}

type pairCodeResource struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type createInstanceBody struct {
	ExternalID *string                `json:"external_id"`
	Name       string                 `json:"name"`
	Settings   instance.SettingsPatch `json:"settings"`
}

type updateInstanceBody struct {
	Name       *string `json:"name"`
	ExternalID *string `json:"external_id"`
}

type pairCodeBody struct {
	Phone string `json:"phone"`
}

func newQRResource(code session.QRCode) qrResource {
	return qrResource{Code: code.Code, ExpiresAt: code.ExpiresAt}
}

func (h *InstancesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body createInstanceBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	created, err := h.instances.Create(r.Context(), clientID(r), instance.CreateInput{
		ExternalID: body.ExternalID,
		Name:       body.Name,
		Settings:   body.Settings,
	})
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Created(w, created)
}

func (h *InstancesHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := domain.InstanceFilter{
		Search:     query.Get("search"),
		Status:     query.Get("status"),
		ExternalID: query.Get("external_id"),
	}
	page, err := h.instances.List(r.Context(), clientID(r), filter, apihttp.ParsePageParams(r))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, page)
}

func (h *InstancesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	found, err := h.instances.Get(r.Context(), clientID(r), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, found)
}

func (h *InstancesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body updateInstanceBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	updated, err := h.instances.Update(r.Context(), clientID(r), id, instance.UpdateInput{
		Name:       body.Name,
		ExternalID: body.ExternalID,
	})
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, updated)
}

func (h *InstancesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.owned(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	if err := h.sessions.Delete(r.Context(), id); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.NoContent(w)
}

func (h *InstancesHandler) Connect(w http.ResponseWriter, r *http.Request) {
	id, current, err := h.owned(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	result, err := h.sessions.Connect(r.Context(), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	response := connectResource{Instance: refreshed(current, result.Instance)}
	if result.QR != nil {
		qr := newQRResource(*result.QR)
		response.QR = &qr
	}
	apihttp.OK(w, response)
}

func (h *InstancesHandler) QR(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.owned(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	code, err := h.sessions.QR(r.Context(), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, qrCodeResource{Status: qrRequiredStatus, QR: newQRResource(code)})
}

func (h *InstancesHandler) PairCode(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.owned(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	var body pairCodeBody
	if err := decodeBody(w, r, &body, bodyOptions{}); err != nil {
		apihttp.WriteError(w, err)
		return
	}
	code, err := h.sessions.PairPhone(r.Context(), id, body.Phone)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, pairCodeResource{Code: code.Code, ExpiresAt: code.ExpiresAt})
}

func (h *InstancesHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.sessions.Disconnect)
}

func (h *InstancesHandler) Logout(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.sessions.Logout)
}

func (h *InstancesHandler) transition(
	w http.ResponseWriter,
	r *http.Request,
	change func(ctx context.Context, id uuid.UUID) (*domain.Instance, error),
) {
	id, current, err := h.owned(r)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	updated, err := change(r.Context(), id)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, refreshed(current, updated))
}

func (h *InstancesHandler) owned(r *http.Request) (uuid.UUID, instance.Resource, error) {
	id, err := pathUUID(r, "instance_id")
	if err != nil {
		return uuid.Nil, instance.Resource{}, err
	}
	current, err := h.instances.Get(r.Context(), clientID(r), id)
	if err != nil {
		return uuid.Nil, instance.Resource{}, err
	}
	return id, current, nil
}

func refreshed(current instance.Resource, updated *domain.Instance) instance.Resource {
	if updated == nil {
		return current
	}
	resource := instance.NewResource(*updated, domain.InstanceSettings{})
	resource.Settings = current.Settings
	return resource
}
