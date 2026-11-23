package instance

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	maxNameLength       = 120
	maxExternalIDLength = 200
)

type Options struct {
	Unit      UnitOfWork
	Instances InstanceStore
	Settings  SettingsStore
}

type Service struct {
	unit      UnitOfWork
	instances InstanceStore
	settings  SettingsStore
}

func NewService(options Options) *Service {
	return &Service{unit: options.Unit, instances: options.Instances, settings: options.Settings}
}

type CreateInput struct {
	ExternalID *string
	Name       string
	Settings   SettingsPatch
}

type UpdateInput struct {
	Name       *string
	ExternalID *string
}

func (s *Service) Create(ctx context.Context, clientID string, input CreateInput) (Resource, error) {
	name, err := validName(input.Name)
	if err != nil {
		return Resource{}, err
	}
	if err := validateExternalID(input.ExternalID); err != nil {
		return Resource{}, err
	}
	item := &domain.Instance{
		ID:          uuid.New(),
		ClientID:    clientID,
		ExternalID:  input.ExternalID,
		Name:        name,
		Status:      domain.InstanceStatusCreated,
		AutoConnect: false,
	}
	settings := defaultSettings(item.ID)
	input.Settings.apply(&settings)
	err = s.unit.Do(ctx, func(unit Unit) error {
		if err := unit.Instances.Insert(ctx, item); err != nil {
			return err
		}
		return unit.Settings.Insert(ctx, &settings)
	})
	if err != nil {
		return Resource{}, err
	}
	return NewResource(*item, settings), nil
}

func (s *Service) Get(ctx context.Context, clientID string, id uuid.UUID) (Resource, error) {
	item, err := s.instances.FindByID(ctx, clientID, id)
	if err != nil {
		return Resource{}, err
	}
	settings, err := s.settings.FindByInstanceID(ctx, item.ID)
	if err != nil {
		return Resource{}, err
	}
	return NewResource(*item, *settings), nil
}

func (s *Service) List(
	ctx context.Context,
	clientID string,
	filter domain.InstanceFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[Resource], error) {
	if err := validateStatusFilter(filter.Status); err != nil {
		return apihttp.PageResult[Resource]{}, err
	}
	page, err := s.instances.ListByClient(ctx, clientID, filter, params)
	if err != nil {
		return apihttp.PageResult[Resource]{}, err
	}
	ids := make([]uuid.UUID, 0, len(page.Data))
	for _, item := range page.Data {
		ids = append(ids, item.ID)
	}
	settings, err := s.settings.FindByInstanceIDs(ctx, ids)
	if err != nil {
		return apihttp.PageResult[Resource]{}, err
	}
	resources := make([]Resource, 0, len(page.Data))
	for _, item := range page.Data {
		resources = append(resources, NewResource(item, settingsOrDefault(settings, item.ID)))
	}
	return apihttp.PageResult[Resource]{
		Data:       resources,
		Limit:      page.Limit,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

func settingsOrDefault(byInstance map[uuid.UUID]domain.InstanceSettings, id uuid.UUID) domain.InstanceSettings {
	if found, ok := byInstance[id]; ok {
		return found
	}
	return defaultSettings(id)
}

func (s *Service) Update(ctx context.Context, clientID string, id uuid.UUID, input UpdateInput) (Resource, error) {
	update, err := validUpdate(input)
	if err != nil {
		return Resource{}, err
	}
	item, err := s.instances.Update(ctx, clientID, id, update)
	if err != nil {
		return Resource{}, err
	}
	settings, err := s.settings.FindByInstanceID(ctx, item.ID)
	if err != nil {
		return Resource{}, err
	}
	return NewResource(*item, *settings), nil
}

func validUpdate(input UpdateInput) (domain.InstanceUpdate, error) {
	update := domain.InstanceUpdate{ExternalID: input.ExternalID}
	if input.Name != nil {
		name, err := validName(*input.Name)
		if err != nil {
			return domain.InstanceUpdate{}, err
		}
		update.Name = &name
	}
	if err := validateExternalID(input.ExternalID); err != nil {
		return domain.InstanceUpdate{}, err
	}
	return update, nil
}

func validName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return "", domain.BadRequest("name must have 1 to 120 characters")
	}
	return name, nil
}

func validateExternalID(externalID *string) error {
	if externalID == nil {
		return nil
	}
	if *externalID == "" || utf8.RuneCountInString(*externalID) > maxExternalIDLength {
		return domain.BadRequest("external_id must have 1 to 200 characters")
	}
	return nil
}

func validateStatusFilter(status string) error {
	switch status {
	case "",
		domain.InstanceStatusCreated,
		domain.InstanceStatusConnecting,
		domain.InstanceStatusQRRequired,
		domain.InstanceStatusConnected,
		domain.InstanceStatusDisconnected,
		domain.InstanceStatusLoggedOut,
		domain.InstanceStatusBanned,
		domain.InstanceStatusClientOutdated:
		return nil
	}
	return domain.BadRequest("status is not a valid instance status")
}
