package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const secretBytes = 32

type CreateInput struct {
	URL         string
	Description *string
	Events      []string
	InstanceIDs []uuid.UUID
	Active      *bool
}

type ServiceOptions struct {
	Webhooks         WebhookStore
	Deliveries       DeliveryStore
	Instances        InstanceReader
	AllowInsecureURL bool
	NewSecret        func() (string, error)
	Now              func() time.Time
}

type Service struct {
	webhooks         WebhookStore
	deliveries       DeliveryStore
	instances        InstanceReader
	allowInsecureURL bool
	newSecret        func() (string, error)
	now              func() time.Time
}

func NewService(options ServiceOptions) *Service {
	if options.NewSecret == nil {
		options.NewSecret = randomSecret
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{
		webhooks:         options.Webhooks,
		deliveries:       options.Deliveries,
		instances:        options.Instances,
		allowInsecureURL: options.AllowInsecureURL,
		newSecret:        options.NewSecret,
		now:              options.Now,
	}
}

func randomSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("webhook: generate secret: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (s *Service) Create(ctx context.Context, clientID string, input CreateInput) (CreatedResource, error) {
	if err := s.validateCreate(ctx, clientID, input); err != nil {
		return CreatedResource{}, err
	}
	secret, err := s.newSecret()
	if err != nil {
		return CreatedResource{}, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	item := &domain.Webhook{
		ID:          uuid.New(),
		ClientID:    clientID,
		URL:         input.URL,
		Description: input.Description,
		Secret:      secret,
		Events:      input.Events,
		InstanceIDs: input.InstanceIDs,
		Active:      active,
	}
	if err := s.webhooks.Insert(ctx, item); err != nil {
		return CreatedResource{}, err
	}
	return CreatedResource{Resource: newResource(*item), Secret: secret}, nil
}

func (s *Service) validateCreate(ctx context.Context, clientID string, input CreateInput) error {
	if err := s.validateURL(input.URL); err != nil {
		return err
	}
	if err := validateEvents(input.Events); err != nil {
		return err
	}
	return s.validateInstances(ctx, clientID, input.InstanceIDs)
}

func (s *Service) Get(ctx context.Context, clientID string, id uuid.UUID) (Resource, error) {
	item, err := s.webhooks.FindByID(ctx, clientID, id)
	if err != nil {
		return Resource{}, err
	}
	return newResource(*item), nil
}

func (s *Service) List(
	ctx context.Context,
	clientID string,
	active *bool,
	params apihttp.PageParams,
) (apihttp.PageResult[Resource], error) {
	page, err := s.webhooks.ListByClient(ctx, clientID, active, params)
	if err != nil {
		return apihttp.PageResult[Resource]{}, err
	}
	resources := make([]Resource, 0, len(page.Data))
	for _, item := range page.Data {
		resources = append(resources, newResource(item))
	}
	return apihttp.PageResult[Resource]{
		Data:       resources,
		Limit:      page.Limit,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

func (s *Service) Update(ctx context.Context, clientID string, id uuid.UUID, update domain.WebhookUpdate) (Resource, error) {
	if err := s.validateUpdate(ctx, clientID, update); err != nil {
		return Resource{}, err
	}
	item, err := s.webhooks.Update(ctx, clientID, id, update)
	if err != nil {
		return Resource{}, err
	}
	return newResource(*item), nil
}

func (s *Service) validateUpdate(ctx context.Context, clientID string, update domain.WebhookUpdate) error {
	if update.URL != nil {
		if err := s.validateURL(*update.URL); err != nil {
			return err
		}
	}
	if update.Events != nil {
		if err := validateEvents(*update.Events); err != nil {
			return err
		}
	}
	if update.InstanceIDs != nil {
		return s.validateInstances(ctx, clientID, *update.InstanceIDs)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, clientID string, id uuid.UUID) error {
	return s.webhooks.SoftDelete(ctx, clientID, id)
}

func (s *Service) RotateSecret(ctx context.Context, clientID string, id uuid.UUID) (SecretResource, error) {
	secret, err := s.newSecret()
	if err != nil {
		return SecretResource{}, err
	}
	if err := s.webhooks.RotateSecret(ctx, clientID, id, secret); err != nil {
		return SecretResource{}, err
	}
	return SecretResource{ID: id, Secret: secret}, nil
}

func (s *Service) SendTest(ctx context.Context, clientID string, id uuid.UUID) (uuid.UUID, error) {
	item, err := s.webhooks.FindByID(ctx, clientID, id)
	if err != nil {
		return uuid.Nil, err
	}
	eventID := uuid.New()
	body, err := s.testEnvelope(eventID, item.ID)
	if err != nil {
		return uuid.Nil, err
	}
	delivery := &domain.WebhookDelivery{
		ID:        uuid.New(),
		WebhookID: item.ID,
		EventID:   eventID,
		EventType: domain.EventTypeWebhookTest,
		Payload:   body,
	}
	if _, err := s.deliveries.Insert(ctx, delivery); err != nil {
		return uuid.Nil, err
	}
	return delivery.ID, nil
}

func (s *Service) testEnvelope(eventID, webhookID uuid.UUID) ([]byte, error) {
	data, err := json.Marshal(map[string]string{"webhook_id": webhookID.String()})
	if err != nil {
		return nil, fmt.Errorf("webhook: encode test data: %w", err)
	}
	body, err := json.Marshal(domain.EventEnvelope{
		EventID:      eventID,
		EventType:    domain.EventTypeWebhookTest,
		EventVersion: 1,
		OccurredAt:   s.now().UTC(),
		Data:         data,
	})
	if err != nil {
		return nil, fmt.Errorf("webhook: encode test envelope: %w", err)
	}
	return body, nil
}

func (s *Service) ListDeliveries(
	ctx context.Context,
	clientID string,
	webhookID uuid.UUID,
	filter domain.DeliveryFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.WebhookDelivery], error) {
	if _, err := s.webhooks.FindByID(ctx, clientID, webhookID); err != nil {
		return apihttp.PageResult[domain.WebhookDelivery]{}, err
	}
	return s.deliveries.ListByWebhook(ctx, clientID, webhookID, filter, params)
}

func (s *Service) RetryDelivery(ctx context.Context, clientID string, webhookID, id uuid.UUID) (*domain.WebhookDelivery, error) {
	return s.deliveries.ResetForRetry(ctx, clientID, webhookID, id)
}
