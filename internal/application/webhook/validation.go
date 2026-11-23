package webhook

import (
	"context"
	"net/url"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const maxURLLength = 2048

func (s *Service) validateURL(raw string) error {
	if raw == "" {
		return domain.BadRequest("url is required")
	}
	if len(raw) > maxURLLength {
		return domain.BadRequest("url exceeds 2048 characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return domain.BadRequest("url is invalid")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if s.allowInsecureURL {
			return nil
		}
		return domain.BadRequest("url must use https")
	default:
		return domain.BadRequest("url must use https")
	}
}

func validateEvents(events []string) error {
	for _, eventType := range events {
		if !domain.IsCatalogEventType(eventType) {
			return domain.BadRequest("unknown event type: " + eventType)
		}
	}
	return nil
}

func (s *Service) validateInstances(ctx context.Context, clientID string, ids []uuid.UUID) error {
	for _, id := range ids {
		if _, err := s.instances.FindByID(ctx, clientID, id); err != nil {
			if appErr, ok := domain.AsAppError(err); ok && appErr.Code == domain.InstanceNotFound().Code {
				return domain.BadRequest("instance_ids contains an unknown instance: " + id.String())
			}
			return err
		}
	}
	return nil
}
