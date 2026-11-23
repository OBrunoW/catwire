package instance

import (
	"context"
	"sort"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type fakeDB struct {
	instances map[uuid.UUID]*domain.Instance
	settings  map[uuid.UUID]*domain.InstanceSettings
	lastList  domain.InstanceFilter
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		instances: map[uuid.UUID]*domain.Instance{},
		settings:  map[uuid.UUID]*domain.InstanceSettings{},
	}
}

func (d *fakeDB) Do(_ context.Context, run func(unit Unit) error) error {
	return run(Unit{Instances: d, Settings: fakeSettings{db: d}})
}

func (d *fakeDB) Insert(_ context.Context, item *domain.Instance) error {
	if item.ExternalID != nil {
		for _, existing := range d.instances {
			if existing.ClientID == item.ClientID && existing.ExternalID != nil && *existing.ExternalID == *item.ExternalID {
				return domain.InstanceAlreadyExists()
			}
		}
	}
	stored := *item
	d.instances[item.ID] = &stored
	return nil
}

func (d *fakeDB) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	item, found := d.instances[id]
	if !found || item.ClientID != clientID {
		return nil, domain.InstanceNotFound()
	}
	copied := *item
	return &copied, nil
}

func (d *fakeDB) ListByClient(
	_ context.Context,
	clientID string,
	filter domain.InstanceFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.Instance], error) {
	d.lastList = filter
	items := []domain.Instance{}
	for _, item := range d.instances {
		if item.ClientID == clientID && (filter.Status == "" || item.Status == filter.Status) {
			items = append(items, *item)
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Name < items[b].Name })
	return apihttp.BuildPage(items, params.Limit, func(last domain.Instance) string { return last.Name }), nil
}

func (d *fakeDB) Update(_ context.Context, clientID string, id uuid.UUID, update domain.InstanceUpdate) (*domain.Instance, error) {
	item, found := d.instances[id]
	if !found || item.ClientID != clientID {
		return nil, domain.InstanceNotFound()
	}
	if update.ExternalID != nil {
		for _, existing := range d.instances {
			if existing.ID != id && existing.ClientID == clientID && existing.ExternalID != nil && *existing.ExternalID == *update.ExternalID {
				return nil, domain.InstanceAlreadyExists()
			}
		}
		item.ExternalID = update.ExternalID
	}
	if update.Name != nil {
		item.Name = *update.Name
	}
	copied := *item
	return &copied, nil
}

func (d *fakeDB) addSettings(settings *domain.InstanceSettings) error {
	stored := *settings
	d.settings[settings.InstanceID] = &stored
	return nil
}

type fakeSettings struct{ db *fakeDB }

func (f fakeSettings) Insert(_ context.Context, settings *domain.InstanceSettings) error {
	return f.db.addSettings(settings)
}

func (f fakeSettings) FindByInstanceID(_ context.Context, id uuid.UUID) (*domain.InstanceSettings, error) {
	item, found := f.db.settings[id]
	if !found {
		return nil, domain.InstanceNotFound()
	}
	copied := *item
	return &copied, nil
}

func (f fakeSettings) FindByInstanceIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.InstanceSettings, error) {
	found := map[uuid.UUID]domain.InstanceSettings{}
	for _, id := range ids {
		if item, ok := f.db.settings[id]; ok {
			found[id] = *item
		}
	}
	return found, nil
}

func (f fakeSettings) Update(_ context.Context, settings *domain.InstanceSettings) error {
	stored := *settings
	f.db.settings[settings.InstanceID] = &stored
	return nil
}
