package session

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func (m *Manager) Start(ctx context.Context) error {
	if err := m.reconnectAll(ctx, shouldReconnectOnStartup); err != nil {
		return err
	}
	if err := m.startWatchdog(); err != nil {
		return err
	}
	return nil
}

func (m *Manager) startWatchdog() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrStopped
	}
	m.wg.Add(1)
	go m.watchdog()
	return nil
}

func shouldReconnectOnStartup(instance domain.Instance) bool {
	switch instance.Status {
	case domain.InstanceStatusBanned, domain.InstanceStatusLoggedOut:
		return false
	}
	return true
}

func shouldReconnectOnWatchdog(instance domain.Instance) bool {
	if instance.Status == domain.InstanceStatusClientOutdated {
		return false
	}
	return shouldReconnectOnStartup(instance)
}

func (m *Manager) watchdog() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.reconnectAll(m.ctx, shouldReconnectOnWatchdog); err != nil {
				m.logger.Warn("watchdog failed to list instances", "error", err)
			}
		}
	}
}

func (m *Manager) reconnectAll(ctx context.Context, eligible func(domain.Instance) bool) error {
	instances, err := m.instances.ListReconnectable(ctx)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if ctx.Err() != nil {
			return nil
		}
		if !eligible(instance) {
			continue
		}
		if err := m.reconnect(ctx, instance.ID, eligible); err != nil {
			m.logger.Warn("reconnect failed", "instance_id", instance.ID, "error", err)
		}
	}
	return nil
}

func (m *Manager) reconnect(ctx context.Context, id uuid.UUID, eligible func(domain.Instance) bool) error {
	current, err := m.entryFor(id)
	if err != nil {
		return err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.unavailable() {
		return nil
	}
	instance, err := m.instances.FindUnscoped(ctx, id)
	if isInstanceNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if instance.JID == nil || !instance.AutoConnect || !eligible(*instance) {
		return nil
	}
	err = m.bringUp(ctx, current, *instance)
	if !isDeviceNotFound(err) {
		return err
	}
	return m.markDeviceMissing(ctx, *instance)
}
