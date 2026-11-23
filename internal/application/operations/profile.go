package operations

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	maxPushNameRunes      = 25
	maxStatusMessageRunes = 139
)

type Profile struct {
	PushName      string
	StatusMessage string
	PictureURL    *string
	BusinessName  *string
}

type ProfileUpdate struct {
	PushName      *string
	StatusMessage *string
}

type ProfileService struct {
	instances InstanceStore
	gateway   ProfileGateway
	logger    *slog.Logger
}

func NewProfileService(instances InstanceStore, gateway ProfileGateway, logger *slog.Logger) *ProfileService {
	return &ProfileService{instances: instances, gateway: gateway, logger: logger}
}

func (s *ProfileService) Get(ctx context.Context, clientID string, instanceID uuid.UUID) (Profile, error) {
	instance, err := findPairedConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return Profile{}, err
	}
	return s.read(ctx, instance)
}

func (s *ProfileService) Update(ctx context.Context, clientID string, instanceID uuid.UUID, update ProfileUpdate) (Profile, error) {
	normalized, err := validateProfileUpdate(update)
	if err != nil {
		return Profile{}, err
	}
	instance, err := findPairedConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return Profile{}, err
	}
	if err := s.apply(ctx, instance.ID, normalized); err != nil {
		return Profile{}, err
	}
	profile, err := s.read(ctx, instance)
	if err != nil {
		return Profile{}, err
	}
	if normalized.PushName != nil {
		profile.PushName = *normalized.PushName
	}
	return profile, nil
}

func (s *ProfileService) apply(ctx context.Context, instanceID uuid.UUID, update ProfileUpdate) error {
	if update.StatusMessage != nil {
		if err := s.gateway.SetStatusMessage(ctx, instanceID, *update.StatusMessage); err != nil {
			return gatewayFailure(s.logger, "set status message", instanceID, err)
		}
	}
	if update.PushName != nil {
		if err := s.gateway.SetPushName(ctx, instanceID, *update.PushName); err != nil {
			return gatewayFailure(s.logger, "set push name", instanceID, err)
		}
	}
	return nil
}

func (s *ProfileService) read(ctx context.Context, instance *domain.Instance) (Profile, error) {
	data, err := s.gateway.GetProfile(ctx, instance.ID)
	if err != nil {
		return Profile{}, gatewayFailure(s.logger, "get profile", instance.ID, err)
	}
	return Profile{
		PushName:      data.PushName,
		StatusMessage: data.StatusMessage,
		PictureURL:    data.PictureURL,
		BusinessName:  instance.BusinessName,
	}, nil
}

func validateProfileUpdate(update ProfileUpdate) (ProfileUpdate, error) {
	if update.PushName == nil && update.StatusMessage == nil {
		return ProfileUpdate{}, domain.BadRequest("push_name or status_message is required")
	}
	normalized := ProfileUpdate{StatusMessage: update.StatusMessage}
	if update.PushName != nil {
		name := strings.TrimSpace(*update.PushName)
		if name == "" || utf8.RuneCountInString(name) > maxPushNameRunes {
			return ProfileUpdate{}, domain.BadRequest("push_name must have 1 to 25 characters")
		}
		normalized.PushName = &name
	}
	if update.StatusMessage != nil {
		message := strings.TrimSpace(*update.StatusMessage)
		if utf8.RuneCountInString(message) > maxStatusMessageRunes {
			return ProfileUpdate{}, domain.BadRequest("status_message must have at most 139 characters")
		}
		normalized.StatusMessage = &message
	}
	return normalized, nil
}
