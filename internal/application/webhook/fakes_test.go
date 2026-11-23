package webhook

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type fakeWebhooks struct {
	items       map[uuid.UUID]*domain.Webhook
	lastSecret  string
	lastUpdate  domain.WebhookUpdate
	deletedByID map[uuid.UUID]bool
}

func newFakeWebhooks() *fakeWebhooks {
	return &fakeWebhooks{items: map[uuid.UUID]*domain.Webhook{}, deletedByID: map[uuid.UUID]bool{}}
}

func (w *fakeWebhooks) Insert(_ context.Context, item *domain.Webhook) error {
	stored := *item
	w.items[item.ID] = &stored
	return nil
}

func (w *fakeWebhooks) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Webhook, error) {
	item, found := w.items[id]
	if !found || item.ClientID != clientID || w.deletedByID[id] {
		return nil, domain.WebhookNotFound()
	}
	copied := *item
	return &copied, nil
}

func (w *fakeWebhooks) ListByClient(_ context.Context, clientID string, _ *bool, params apihttp.PageParams) (apihttp.PageResult[domain.Webhook], error) {
	items := []domain.Webhook{}
	for _, item := range w.items {
		if item.ClientID == clientID {
			items = append(items, *item)
		}
	}
	return apihttp.PageResult[domain.Webhook]{Data: items, Limit: params.Limit}, nil
}

func (w *fakeWebhooks) Update(_ context.Context, clientID string, id uuid.UUID, update domain.WebhookUpdate) (*domain.Webhook, error) {
	item, found := w.items[id]
	if !found || item.ClientID != clientID {
		return nil, domain.WebhookNotFound()
	}
	w.lastUpdate = update
	if update.URL != nil {
		item.URL = *update.URL
	}
	copied := *item
	return &copied, nil
}

func (w *fakeWebhooks) RotateSecret(_ context.Context, clientID string, id uuid.UUID, secret string) error {
	item, found := w.items[id]
	if !found || item.ClientID != clientID {
		return domain.WebhookNotFound()
	}
	item.Secret = secret
	w.lastSecret = secret
	return nil
}

func (w *fakeWebhooks) SoftDelete(_ context.Context, clientID string, id uuid.UUID) error {
	item, found := w.items[id]
	if !found || item.ClientID != clientID || w.deletedByID[id] {
		return domain.WebhookNotFound()
	}
	w.deletedByID[id] = true
	return nil
}

type fakeDeliveries struct {
	inserted []domain.WebhookDelivery
	reset    []uuid.UUID
}

func (d *fakeDeliveries) Insert(_ context.Context, delivery *domain.WebhookDelivery) (bool, error) {
	d.inserted = append(d.inserted, *delivery)
	return true, nil
}

func (d *fakeDeliveries) ListByWebhook(
	_ context.Context, _ string, _ uuid.UUID, _ domain.DeliveryFilter, params apihttp.PageParams,
) (apihttp.PageResult[domain.WebhookDelivery], error) {
	return apihttp.PageResult[domain.WebhookDelivery]{Data: d.inserted, Limit: params.Limit}, nil
}

func (d *fakeDeliveries) ResetForRetry(_ context.Context, _ string, webhookID, id uuid.UUID) (*domain.WebhookDelivery, error) {
	d.reset = append(d.reset, id)
	return &domain.WebhookDelivery{ID: id, WebhookID: webhookID, Status: domain.DeliveryStatusPending}, nil
}

type fakeInstances struct {
	owned map[uuid.UUID]string
}

func (i *fakeInstances) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	if i.owned[id] != clientID {
		return nil, domain.InstanceNotFound()
	}
	return &domain.Instance{ID: id, ClientID: clientID}, nil
}

type fakeDispatchStore struct {
	mu       sync.Mutex
	due      []domain.DueDelivery
	success  map[uuid.UUID]int
	failures map[uuid.UUID]recordedFailure
	attempts map[uuid.UUID]int
}

type recordedFailure struct {
	statusCode *int
	message    string
	next       time.Time
	status     string
}

func newFakeDispatchStore(due ...domain.DueDelivery) *fakeDispatchStore {
	attempts := map[uuid.UUID]int{}
	for _, item := range due {
		attempts[item.ID] = item.Attempts
	}
	return &fakeDispatchStore{due: due, success: map[uuid.UUID]int{}, failures: map[uuid.UUID]recordedFailure{}, attempts: attempts}
}

func (s *fakeDispatchStore) ClaimDue(_ context.Context, limit int, _ time.Duration) ([]domain.DueDelivery, error) {
	claimed := s.due
	if len(claimed) > limit {
		claimed = claimed[:limit]
	}
	s.due = nil
	return claimed, nil
}

func (s *fakeDispatchStore) RecordSuccess(_ context.Context, id uuid.UUID, statusCode int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.success[id] = statusCode
	return nil
}

func (s *fakeDispatchStore) RecordFailure(
	_ context.Context, id uuid.UUID, statusCode *int, message string, next time.Time, maxAttempts int,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := domain.DeliveryStatusFailed
	if s.attempts[id]+1 >= maxAttempts {
		status = domain.DeliveryStatusDead
	}
	s.failures[id] = recordedFailure{statusCode: statusCode, message: message, next: next, status: status}
	return status, nil
}

type fakeSender struct {
	mu       sync.Mutex
	status   int
	err      error
	requests []Request
}

func (s *fakeSender) Send(_ context.Context, request Request) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	return s.status, s.err
}
