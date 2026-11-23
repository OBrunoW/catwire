package session

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var ErrStopped = errors.New("session: manager stopped")

const (
	defaultWatchdogInterval = 60 * time.Second
	defaultQRWait           = 15 * time.Second
	pairCodeTTL             = 2 * time.Minute
	minPhoneDigits          = 8
	maxPhoneDigits          = 15
)

type Options struct {
	Instances        InstanceReader
	Unit             UnitOfWork
	Clients          ClientFactory
	Logger           *slog.Logger
	Now              func() time.Time
	WatchdogInterval time.Duration
	QRWait           time.Duration
}

type ConnectResult struct {
	Instance *domain.Instance
	QR       *QRCode
}

type PairCode struct {
	Code      string
	ExpiresAt time.Time
}

type Manager struct {
	instances        InstanceReader
	unit             UnitOfWork
	clients          ClientFactory
	logger           *slog.Logger
	now              func() time.Time
	watchdogInterval time.Duration
	qrWait           time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	stopped bool
	entries map[uuid.UUID]*entry
}

func NewManager(options Options) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		instances:        options.Instances,
		unit:             options.Unit,
		clients:          options.Clients,
		logger:           options.Logger,
		now:              options.Now,
		watchdogInterval: options.WatchdogInterval,
		qrWait:           options.QRWait,
		ctx:              ctx,
		cancel:           cancel,
		entries:          map[uuid.UUID]*entry{},
	}
	if manager.logger == nil {
		manager.logger = slog.Default()
	}
	if manager.now == nil {
		manager.now = time.Now
	}
	if manager.watchdogInterval <= 0 {
		manager.watchdogInterval = defaultWatchdogInterval
	}
	if manager.qrWait <= 0 {
		manager.qrWait = defaultQRWait
	}
	return manager
}

func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	entries := make([]*entry, 0, len(m.entries))
	for id, current := range m.entries {
		entries = append(entries, current)
		delete(m.entries, id)
	}
	m.mu.Unlock()
	m.cancel()
	for _, current := range entries {
		current.mu.Lock()
		current.stopped = true
		if current.client != nil {
			current.client.Disconnect()
		}
		current.releaseClient()
		current.mu.Unlock()
	}
	m.wg.Wait()
}

func (m *Manager) entryFor(id uuid.UUID) (*entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, ErrStopped
	}
	if current, found := m.entries[id]; found {
		return current, nil
	}
	current := newEntry(m.ctx, id)
	m.entries[id] = current
	m.wg.Add(1)
	go m.runEvents(current)
	return current, nil
}

func (m *Manager) forget(e *entry) {
	m.mu.Lock()
	if m.entries[e.instanceID] == e {
		delete(m.entries, e.instanceID)
	}
	m.mu.Unlock()
}

func (m *Manager) runEvents(e *entry) {
	defer m.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case queued := <-e.events:
			m.handleQueuedEvent(e, queued)
		}
	}
}

func (m *Manager) lock(ctx context.Context, id uuid.UUID) (*entry, *domain.Instance, error) {
	current, err := m.entryFor(id)
	if err != nil {
		return nil, nil, err
	}
	current.mu.Lock()
	if current.stopped {
		current.mu.Unlock()
		return nil, nil, ErrStopped
	}
	if current.dropped {
		current.mu.Unlock()
		return nil, nil, domain.InstanceNotFound()
	}
	instance, err := m.instances.FindUnscoped(ctx, id)
	if err != nil {
		if isInstanceNotFound(err) {
			m.drop(current)
		}
		current.mu.Unlock()
		return nil, nil, err
	}
	return current, instance, nil
}

func (m *Manager) drop(e *entry) {
	e.dropped = true
	e.cancel()
	m.forget(e)
}

func (m *Manager) reload(ctx context.Context, id uuid.UUID) (*domain.Instance, error) {
	return m.instances.FindUnscoped(ctx, id)
}

func (m *Manager) ensureClient(ctx context.Context, e *entry, instance domain.Instance) error {
	if e.client != nil {
		return nil
	}
	generation := e.generation.Load() + 1
	client, err := m.clients.Create(ctx, instance, e.emitter(generation))
	if err != nil {
		return err
	}
	e.generation.Store(generation)
	e.client = client
	e.clientCtx, e.clientCancel = context.WithCancel(e.ctx)
	return nil
}

func (m *Manager) Connect(ctx context.Context, id uuid.UUID) (ConnectResult, error) {
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return ConnectResult{}, err
	}
	defer current.mu.Unlock()
	if instance.JID == nil {
		return m.connectUnpaired(ctx, current, *instance)
	}
	if current.client != nil && current.client.IsConnected() && instance.Status == domain.InstanceStatusConnected {
		return ConnectResult{}, domain.InstanceAlreadyConnected()
	}
	return m.connectPaired(ctx, current, *instance)
}

func (m *Manager) connectPaired(ctx context.Context, e *entry, instance domain.Instance) (ConnectResult, error) {
	if err := m.bringUp(ctx, e, instance); err != nil {
		if isDeviceNotFound(err) {
			return m.recoverMissingDevice(ctx, e, instance)
		}
		return ConnectResult{}, err
	}
	updated, err := m.reload(ctx, instance.ID)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{Instance: updated}, nil
}

func (m *Manager) recoverMissingDevice(ctx context.Context, e *entry, instance domain.Instance) (ConnectResult, error) {
	if err := m.markDeviceMissing(ctx, instance); err != nil {
		return ConnectResult{}, err
	}
	updated, err := m.reload(ctx, instance.ID)
	if err != nil {
		return ConnectResult{}, err
	}
	return m.connectUnpaired(ctx, e, *updated)
}

func (m *Manager) markDeviceMissing(ctx context.Context, instance domain.Instance) error {
	return m.transition(ctx, instance, transition{
		status:           domain.InstanceStatusLoggedOut,
		reason:           reasonDeviceMissing,
		markDisconnected: true,
		identity:         clearedIdentity(),
		autoConnect:      boolPointer(false),
	})
}

func (m *Manager) bringUp(ctx context.Context, e *entry, instance domain.Instance) error {
	if err := m.ensureClient(ctx, e, instance); err != nil {
		if isDeviceNotFound(err) {
			return ErrDeviceNotFound
		}
		return domain.WhatsAppError("create client: " + err.Error())
	}
	if e.client.IsConnected() {
		return nil
	}
	if err := e.client.Connect(); err != nil {
		return domain.WhatsAppError("connect: " + err.Error())
	}
	return m.transition(ctx, instance, transition{
		status:      domain.InstanceStatusConnecting,
		autoConnect: boolPointer(true),
	})
}

func (m *Manager) connectUnpaired(ctx context.Context, e *entry, instance domain.Instance) (ConnectResult, error) {
	if e.client != nil {
		if code, active := e.currentQR(m.now()); active && instance.Status == domain.InstanceStatusQRRequired {
			return ConnectResult{Instance: &instance, QR: &code}, nil
		}
		e.client.Disconnect()
		e.releaseClient()
	}
	if err := m.ensureClient(ctx, e, instance); err != nil {
		return ConnectResult{}, domain.WhatsAppError("create client: " + err.Error())
	}
	items, err := e.client.OpenQRChannel(e.clientCtx)
	if err != nil {
		e.releaseClient()
		return ConnectResult{}, domain.WhatsAppError("open qr channel: " + err.Error())
	}
	if err := e.client.Connect(); err != nil {
		e.releaseClient()
		return ConnectResult{}, domain.WhatsAppError("connect: " + err.Error())
	}
	m.wg.Add(1)
	go m.consumeQR(e.clientCtx, e, e.generation.Load(), items)
	err = m.transition(ctx, instance, transition{
		status:      domain.InstanceStatusQRRequired,
		autoConnect: boolPointer(true),
	})
	if err != nil {
		return ConnectResult{}, m.abortQR(ctx, e, instance, err)
	}
	return m.awaitFirstQR(ctx, e, instance)
}

func (m *Manager) awaitFirstQR(ctx context.Context, e *entry, instance domain.Instance) (ConnectResult, error) {
	timer := time.NewTimer(m.qrWait)
	defer timer.Stop()
	select {
	case <-e.firstQRSignal():
	case <-timer.C:
		return ConnectResult{}, m.abortQR(ctx, e, instance, domain.WhatsAppError("qr code not received"))
	case <-ctx.Done():
		return ConnectResult{}, m.abortQR(ctx, e, instance, ctx.Err())
	case <-e.ctx.Done():
		return ConnectResult{}, m.abortQR(ctx, e, instance, ErrStopped)
	}
	code, active := e.currentQR(m.now())
	if !active {
		return ConnectResult{}, m.abortQR(ctx, e, instance, domain.QRExpired())
	}
	updated, err := m.reload(ctx, instance.ID)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{Instance: updated, QR: &code}, nil
}

func (m *Manager) abortQR(ctx context.Context, e *entry, instance domain.Instance, cause error) error {
	e.client.Disconnect()
	e.releaseClient()
	revert := transition{
		status:           domain.InstanceStatusDisconnected,
		reason:           reasonQRUnavailable,
		markDisconnected: true,
	}
	if err := m.transition(context.WithoutCancel(ctx), instance, revert); err != nil {
		m.logger.Error("revert qr transition failed", "instance_id", instance.ID, "error", err)
	}
	return cause
}

func (m *Manager) consumeQR(ctx context.Context, e *entry, generation uint64, items <-chan QRItem) {
	defer m.wg.Done()
	emit := e.emitter(generation)
	for {
		select {
		case <-ctx.Done():
			return
		case item, open := <-items:
			if !open {
				return
			}
			m.handleQRItem(e, emit, generation, item)
		}
	}
}

func (m *Manager) handleQRItem(e *entry, emit func(Event), generation uint64, item QRItem) {
	if e.generation.Load() != generation {
		return
	}
	switch item.Kind {
	case QRItemCode:
		e.setQR(generation, item.Code, m.now().Add(item.Timeout))
	case QRItemTimeout:
		emit(Event{Kind: EventQRTimeout})
	case QRItemClientOutdated:
		emit(Event{Kind: EventClientOutdated})
	case QRItemFailed:
		emit(Event{Kind: EventQRFailed, Reason: item.Reason})
	}
}

func (m *Manager) QR(ctx context.Context, id uuid.UUID) (QRCode, error) {
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return QRCode{}, err
	}
	defer current.mu.Unlock()
	if instance.Status == domain.InstanceStatusConnected {
		return QRCode{}, domain.InstanceAlreadyConnected()
	}
	if instance.Status != domain.InstanceStatusQRRequired {
		return QRCode{}, domain.QRExpired()
	}
	code, active := current.currentQR(m.now())
	if !active {
		return QRCode{}, domain.QRExpired()
	}
	return code, nil
}

func (m *Manager) PairPhone(ctx context.Context, id uuid.UUID, phone string) (PairCode, error) {
	if !validPhone(phone) {
		return PairCode{}, domain.BadRequest("phone must contain only digits with country code")
	}
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return PairCode{}, err
	}
	defer current.mu.Unlock()
	if instance.Status == domain.InstanceStatusConnected {
		return PairCode{}, domain.InstanceAlreadyConnected()
	}
	if instance.Status != domain.InstanceStatusQRRequired || current.client == nil {
		return PairCode{}, domain.BadRequest("instance must be in qr_required; call connect first")
	}
	code, err := current.client.PairPhone(ctx, phone)
	if err != nil {
		return PairCode{}, domain.WhatsAppError("pair phone: " + err.Error())
	}
	return PairCode{Code: code, ExpiresAt: m.now().Add(pairCodeTTL)}, nil
}

func validPhone(phone string) bool {
	if len(phone) < minPhoneDigits || len(phone) > maxPhoneDigits {
		return false
	}
	for _, char := range phone {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (m *Manager) Disconnect(ctx context.Context, id uuid.UUID) (*domain.Instance, error) {
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	defer current.mu.Unlock()
	if current.client == nil && instance.JID == nil {
		return instance, nil
	}
	err = m.transition(ctx, *instance, transition{
		status:           domain.InstanceStatusDisconnected,
		reason:           reasonDisconnectRequest,
		markDisconnected: true,
		autoConnect:      boolPointer(false),
	})
	if err != nil {
		return nil, err
	}
	if current.client != nil {
		current.client.Disconnect()
		current.releaseClient()
	}
	return m.reload(ctx, id)
}

func (m *Manager) Logout(ctx context.Context, id uuid.UUID) (*domain.Instance, error) {
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	defer current.mu.Unlock()
	if instance.JID == nil {
		return nil, domain.InstanceNotPaired()
	}
	if err := m.ensureClient(ctx, current, *instance); err != nil {
		if !isDeviceNotFound(err) {
			return nil, domain.WhatsAppError("create client: " + err.Error())
		}
		return m.finishLogout(ctx, current, *instance, reasonDeviceMissing)
	}
	if err := remoteLogout(ctx, current.client); err != nil {
		return nil, domain.WhatsAppError("logout: " + err.Error())
	}
	return m.finishLogout(ctx, current, *instance, reasonLoggedOutFromApp)
}

func (m *Manager) finishLogout(ctx context.Context, e *entry, instance domain.Instance, reason string) (*domain.Instance, error) {
	if e.client != nil {
		if err := e.client.DeleteDevice(ctx); err != nil {
			return nil, domain.WhatsAppError("delete device: " + err.Error())
		}
	}
	e.releaseClient()
	err := m.transition(ctx, instance, transition{
		status:           domain.InstanceStatusLoggedOut,
		reason:           reason,
		markDisconnected: true,
		identity:         clearedIdentity(),
		autoConnect:      boolPointer(false),
	})
	if err != nil {
		return nil, err
	}
	return m.reload(ctx, instance.ID)
}

func remoteLogout(ctx context.Context, client Client) error {
	if !client.IsConnected() {
		if err := client.Connect(); err != nil {
			return err
		}
	}
	return client.Logout(ctx)
}

func (m *Manager) Delete(ctx context.Context, id uuid.UUID) error {
	current, instance, err := m.lock(ctx, id)
	if err != nil {
		return err
	}
	defer current.mu.Unlock()
	if err := m.removeInstance(ctx, *instance); err != nil {
		return err
	}
	if instance.JID != nil {
		m.eraseDevice(ctx, current, *instance)
	}
	if current.client != nil {
		current.client.Disconnect()
		current.releaseClient()
	}
	m.drop(current)
	return nil
}

func (m *Manager) eraseDevice(ctx context.Context, e *entry, instance domain.Instance) {
	err := m.ensureClient(ctx, e, instance)
	if isDeviceNotFound(err) {
		return
	}
	if err != nil {
		m.logger.Warn("create client failed during delete", "instance_id", instance.ID, "error", err)
		return
	}
	if err := remoteLogout(ctx, e.client); err != nil {
		m.logger.Warn("remote logout failed during delete", "instance_id", instance.ID, "error", err)
	}
	e.client.Disconnect()
	if err := e.client.DeleteDevice(ctx); err != nil {
		m.logger.Warn("delete device failed during delete", "instance_id", instance.ID, "error", err)
	}
}

func (m *Manager) removeInstance(ctx context.Context, instance domain.Instance) error {
	return m.unit.Do(ctx, func(unit Unit) error {
		if instance.JID != nil {
			change := transition{
				status:           domain.InstanceStatusLoggedOut,
				reason:           reasonLoggedOutFromApp,
				markDisconnected: true,
				identity:         clearedIdentity(),
			}
			if err := m.recordTransition(ctx, unit, instance, change); err != nil {
				return err
			}
		}
		return unit.Instances.SoftDelete(ctx, instance.ClientID, instance.ID)
	})
}
