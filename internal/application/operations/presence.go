package operations

import (
	"context"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	PresenceComposing = "composing"
	PresenceRecording = "recording"
	PresencePaused    = "paused"
)

var presenceStates = []string{PresenceComposing, PresenceRecording, PresencePaused}

type PresenceService struct {
	instances InstanceStore
	chats     ChatStore
	gateway   PresenceGateway
	logger    *slog.Logger
}

func NewPresenceService(instances InstanceStore, chats ChatStore, gateway PresenceGateway, logger *slog.Logger) *PresenceService {
	return &PresenceService{instances: instances, chats: chats, gateway: gateway, logger: logger}
}

func (s *PresenceService) Send(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, state string) error {
	if !slices.Contains(presenceStates, state) {
		return domain.BadRequest("state must be composing, recording or paused")
	}
	instance, err := findConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return err
	}
	view, err := s.chats.FindByID(ctx, instance.ID, chatID)
	if err != nil {
		return err
	}
	if err := s.gateway.SendPresence(ctx, instance.ID, view.JID, state); err != nil {
		return gatewayFailure(s.logger, "send presence", instance.ID, err)
	}
	return nil
}
