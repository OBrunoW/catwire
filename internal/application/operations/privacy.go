package operations

import (
	"context"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	PrivacyKeyGroupAdd     = "group_add"
	PrivacyKeyLastSeen     = "last_seen"
	PrivacyKeyStatus       = "status"
	PrivacyKeyProfile      = "profile"
	PrivacyKeyReadReceipts = "read_receipts"
	PrivacyKeyOnline       = "online"
	PrivacyKeyCallAdd      = "call_add"
)

var audienceValues = []string{"all", "contacts", "contact_blacklist", "none"}

var privacyKeys = []string{
	PrivacyKeyGroupAdd, PrivacyKeyLastSeen, PrivacyKeyStatus, PrivacyKeyProfile,
	PrivacyKeyReadReceipts, PrivacyKeyOnline, PrivacyKeyCallAdd,
}

var privacyValues = map[string][]string{
	PrivacyKeyGroupAdd:     audienceValues,
	PrivacyKeyLastSeen:     audienceValues,
	PrivacyKeyStatus:       audienceValues,
	PrivacyKeyProfile:      audienceValues,
	PrivacyKeyReadReceipts: {"all", "none"},
	PrivacyKeyOnline:       {"all", "match_last_seen"},
	PrivacyKeyCallAdd:      {"all", "known"},
}

type PrivacySettings struct {
	GroupAdd     string
	LastSeen     string
	Status       string
	Profile      string
	ReadReceipts string
	Online       string
	CallAdd      string
}

type PrivacyUpdate struct {
	GroupAdd     *string
	LastSeen     *string
	Status       *string
	Profile      *string
	ReadReceipts *string
	Online       *string
	CallAdd      *string
}

func (u PrivacyUpdate) changes() []PrivacyChange {
	byKey := map[string]*string{
		PrivacyKeyGroupAdd: u.GroupAdd, PrivacyKeyLastSeen: u.LastSeen, PrivacyKeyStatus: u.Status,
		PrivacyKeyProfile: u.Profile, PrivacyKeyReadReceipts: u.ReadReceipts,
		PrivacyKeyOnline: u.Online, PrivacyKeyCallAdd: u.CallAdd,
	}
	changes := []PrivacyChange{}
	for _, key := range privacyKeys {
		if value := byKey[key]; value != nil {
			changes = append(changes, PrivacyChange{Key: key, Value: *value})
		}
	}
	return changes
}

func validatePrivacyChanges(changes []PrivacyChange) error {
	if len(changes) == 0 {
		return domain.BadRequest("at least one privacy setting is required")
	}
	for _, change := range changes {
		if !slices.Contains(privacyValues[change.Key], change.Value) {
			return domain.BadRequest("invalid value for " + change.Key)
		}
	}
	return nil
}

type PrivacyService struct {
	instances InstanceStore
	gateway   PrivacyGateway
	logger    *slog.Logger
}

func NewPrivacyService(instances InstanceStore, gateway PrivacyGateway, logger *slog.Logger) *PrivacyService {
	return &PrivacyService{instances: instances, gateway: gateway, logger: logger}
}

func (s *PrivacyService) Get(ctx context.Context, clientID string, instanceID uuid.UUID) (PrivacySettings, error) {
	instance, err := findPairedConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return PrivacySettings{}, err
	}
	settings, err := s.gateway.GetPrivacy(ctx, instance.ID)
	if err != nil {
		return PrivacySettings{}, gatewayFailure(s.logger, "get privacy", instance.ID, err)
	}
	return settings, nil
}

func (s *PrivacyService) Update(ctx context.Context, clientID string, instanceID uuid.UUID, update PrivacyUpdate) (PrivacySettings, error) {
	changes := update.changes()
	if err := validatePrivacyChanges(changes); err != nil {
		return PrivacySettings{}, err
	}
	instance, err := findPairedConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return PrivacySettings{}, err
	}
	var settings PrivacySettings
	for _, change := range changes {
		settings, err = s.gateway.SetPrivacy(ctx, instance.ID, change)
		if err != nil {
			return PrivacySettings{}, gatewayFailure(s.logger, "set privacy", instance.ID, err)
		}
	}
	return settings, nil
}
