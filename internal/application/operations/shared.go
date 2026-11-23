package operations

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func findConnected(ctx context.Context, instances InstanceStore, clientID string, id uuid.UUID) (*domain.Instance, error) {
	instance, err := instances.FindByID(ctx, clientID, id)
	if err != nil {
		return nil, err
	}
	if err := ensureConnected(instance); err != nil {
		return nil, err
	}
	return instance, nil
}

func findPairedConnected(ctx context.Context, instances InstanceStore, clientID string, id uuid.UUID) (*domain.Instance, error) {
	instance, err := instances.FindByID(ctx, clientID, id)
	if err != nil {
		return nil, err
	}
	if instance.JID == nil {
		return nil, domain.InstanceNotPaired()
	}
	if err := ensureConnected(instance); err != nil {
		return nil, err
	}
	return instance, nil
}

func ensureConnected(instance *domain.Instance) error {
	if instance.Status != domain.InstanceStatusConnected {
		return domain.InstanceNotConnected()
	}
	return nil
}

func gatewayFailure(logger *slog.Logger, operation string, instanceID uuid.UUID, err error) error {
	if errors.Is(err, ErrInstanceDisconnected) {
		return domain.InstanceNotConnected()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("whatsapp operation failed", "operation", operation, "instance_id", instanceID, "error", err)
	return domain.WhatsAppError("whatsapp rejected the operation")
}
