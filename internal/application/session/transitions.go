package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	EventTypeInstanceStatusChanged = "whatsapp.instance.status_changed"

	reasonPaired             = "paired"
	reasonConnectionLost     = "connection_lost"
	reasonLoggedOutFromApp   = "logged_out_from_api"
	reasonLoggedOutFromPhone = "logged_out_from_phone"
	reasonDeviceMissing      = "device_missing"
	reasonDisconnectRequest  = "disconnect_requested"
	reasonStreamReplaced     = "stream_replaced"
	reasonTemporaryBan       = "temporary_ban"
	reasonBanned             = "banned"
	reasonClientOutdated     = "client_outdated"
	reasonConnectFailure     = "connect_failure"
	reasonQRTimeout          = "qr_timeout"
	reasonQRFailed           = "qr_failed"
	reasonQRUnavailable      = "qr_unavailable"
)

type transition struct {
	status           string
	reason           string
	markConnected    bool
	markDisconnected bool
	identity         *domain.InstanceIdentity
	autoConnect      *bool
}

type statusChangedPayload struct {
	Status         string  `json:"status"`
	PreviousStatus string  `json:"previous_status"`
	Reason         *string `json:"reason"`
	JID            *string `json:"jid"`
	Phone          *string `json:"phone"`
}

func boolPointer(value bool) *bool {
	return &value
}

func (m *Manager) transition(ctx context.Context, instance domain.Instance, change transition) error {
	return m.unit.Do(ctx, func(unit Unit) error {
		return m.recordTransition(ctx, unit, instance, change)
	})
}

func (m *Manager) recordTransition(ctx context.Context, unit Unit, instance domain.Instance, change transition) error {
	now := m.now()
	if change.identity != nil {
		if err := unit.Instances.UpdateIdentity(ctx, instance.ID, *change.identity); err != nil {
			return err
		}
	}
	previous, err := unit.Instances.UpdateStatus(ctx, instance.ID, statusChange(change, now))
	if err != nil {
		return err
	}
	if change.autoConnect != nil {
		if err := unit.Instances.SetAutoConnect(ctx, instance.ID, *change.autoConnect); err != nil {
			return err
		}
	}
	if previous == change.status {
		return nil
	}
	event, err := statusChangedEvent(instance, change, previous, now)
	if err != nil {
		return err
	}
	return unit.Outbox.Insert(ctx, event)
}

func isDeviceNotFound(err error) bool {
	return errors.Is(err, ErrDeviceNotFound)
}

func isInstanceNotFound(err error) bool {
	var appError *domain.AppError
	return errors.As(err, &appError) && appError.Code == domain.InstanceNotFound().Code
}

func statusChange(change transition, now time.Time) domain.InstanceStatusChange {
	result := domain.InstanceStatusChange{Status: change.status}
	if change.reason != "" {
		reason := change.reason
		result.Reason = &reason
	}
	if change.markConnected {
		result.ConnectedAt = &now
	}
	if change.markDisconnected {
		result.DisconnectedAt = &now
	}
	return result
}

func statusChangedEvent(instance domain.Instance, change transition, previous string, now time.Time) (*domain.OutboxEvent, error) {
	payload := statusChangedPayload{
		Status:         change.status,
		PreviousStatus: previous,
		JID:            instance.JID,
		Phone:          instance.Phone,
	}
	if change.reason != "" {
		reason := change.reason
		payload.Reason = &reason
	}
	if change.identity != nil && change.identity.JID != nil {
		payload.JID = change.identity.JID
		payload.Phone = change.identity.Phone
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("session: encode status payload: %w", err)
	}
	instanceID := instance.ID
	return &domain.OutboxEvent{
		EventID:      uuid.New(),
		EventType:    EventTypeInstanceStatusChanged,
		EventVersion: 1,
		ClientID:     instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: instance.ID.String(),
		Payload:      encoded,
		OccurredAt:   now,
	}, nil
}

func clearedIdentity() *domain.InstanceIdentity {
	return &domain.InstanceIdentity{}
}

func (m *Manager) handleQueuedEvent(e *entry, queued queuedEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.unavailable() || queued.generation != e.generation.Load() {
		return
	}
	ctx := e.ctx
	instance, err := m.instances.FindUnscoped(ctx, e.instanceID)
	if err != nil {
		m.logger.Warn("session event dropped", "instance_id", e.instanceID, "kind", queued.event.Kind, "error", err)
		return
	}
	if err := m.applyEvent(ctx, e, *instance, queued.event); err != nil {
		m.logger.Error("session event failed", "instance_id", e.instanceID, "kind", queued.event.Kind, "error", err)
	}
}

func (m *Manager) applyEvent(ctx context.Context, e *entry, instance domain.Instance, event Event) error {
	switch event.Kind {
	case EventConnected:
		return m.transition(ctx, instance, transition{status: domain.InstanceStatusConnected, markConnected: true})
	case EventPaired:
		return m.applyPaired(ctx, e, instance, event)
	case EventDisconnected:
		return m.applyDisconnected(ctx, instance)
	case EventLoggedOut:
		return m.applyLoggedOut(ctx, e, instance, event.Reason)
	case EventStreamReplaced:
		return m.applyTerminal(ctx, e, instance, domain.InstanceStatusDisconnected, reasonStreamReplaced, boolPointer(false))
	case EventTemporaryBan:
		return m.applyTerminal(ctx, e, instance, domain.InstanceStatusBanned, reasonTemporaryBan, boolPointer(false))
	case EventBanned:
		return m.applyTerminal(ctx, e, instance, domain.InstanceStatusBanned, reasonBanned, boolPointer(false))
	case EventClientOutdated:
		return m.applyTerminal(ctx, e, instance, domain.InstanceStatusClientOutdated, reasonClientOutdated, nil)
	case EventConnectFailure:
		return m.applyTerminal(ctx, e, instance, domain.InstanceStatusDisconnected, reasonConnectFailure, nil)
	case EventQRTimeout:
		return m.applyQREnded(ctx, e, instance, reasonQRTimeout)
	case EventQRFailed:
		return m.applyQREnded(ctx, e, instance, reasonQRFailed)
	}
	return fmt.Errorf("session: unknown event kind %d", event.Kind)
}

func (m *Manager) applyPaired(ctx context.Context, e *entry, instance domain.Instance, event Event) error {
	e.resetQR()
	return m.transition(ctx, instance, transition{
		status:      domain.InstanceStatusConnecting,
		reason:      reasonPaired,
		identity:    &event.Identity,
		autoConnect: boolPointer(true),
	})
}

func (m *Manager) applyDisconnected(ctx context.Context, instance domain.Instance) error {
	if instance.JID == nil {
		return nil
	}
	switch instance.Status {
	case domain.InstanceStatusConnected:
	case domain.InstanceStatusConnecting:
		if instance.StatusReason != nil && *instance.StatusReason == reasonPaired {
			return nil
		}
	default:
		return nil
	}
	return m.transition(ctx, instance, transition{
		status:           domain.InstanceStatusDisconnected,
		reason:           reasonConnectionLost,
		markDisconnected: true,
	})
}

func (m *Manager) applyLoggedOut(ctx context.Context, e *entry, instance domain.Instance, reason string) error {
	if reason == "" {
		reason = reasonLoggedOutFromPhone
	}
	if e.client != nil {
		if err := e.client.DeleteDevice(ctx); err != nil {
			m.logger.Warn("delete device after logout failed", "instance_id", instance.ID, "error", err)
		}
	}
	e.releaseClient()
	return m.transition(ctx, instance, transition{
		status:           domain.InstanceStatusLoggedOut,
		reason:           reason,
		markDisconnected: true,
		identity:         clearedIdentity(),
		autoConnect:      boolPointer(false),
	})
}

func (m *Manager) applyTerminal(
	ctx context.Context,
	e *entry,
	instance domain.Instance,
	status, reason string,
	autoConnect *bool,
) error {
	e.releaseClient()
	return m.transition(ctx, instance, transition{
		status:           status,
		reason:           reason,
		markDisconnected: true,
		autoConnect:      autoConnect,
	})
}

func (m *Manager) applyQREnded(ctx context.Context, e *entry, instance domain.Instance, reason string) error {
	if instance.Status != domain.InstanceStatusQRRequired {
		return nil
	}
	return m.applyTerminal(ctx, e, instance, domain.InstanceStatusDisconnected, reason, nil)
}
