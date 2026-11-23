package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type fakeStore struct {
	mu        sync.Mutex
	instances map[uuid.UUID]*domain.Instance
	outbox    []domain.OutboxEvent
	deleted   []uuid.UUID
	doErr     error
}

func newFakeStore(instances ...domain.Instance) *fakeStore {
	store := &fakeStore{instances: map[uuid.UUID]*domain.Instance{}}
	for index := range instances {
		instance := instances[index]
		store.instances[instance.ID] = &instance
	}
	return store
}

func (s *fakeStore) FindUnscoped(_ context.Context, id uuid.UUID) (*domain.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, found := s.instances[id]
	if !found || instance.DeletedAt != nil {
		return nil, domain.InstanceNotFound()
	}
	copied := *instance
	return &copied, nil
}

func (s *fakeStore) ListReconnectable(_ context.Context) ([]domain.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []domain.Instance{}
	for _, instance := range s.instances {
		if instance.JID != nil && instance.AutoConnect && instance.DeletedAt == nil {
			result = append(result, *instance)
		}
	}
	return result, nil
}

func (s *fakeStore) UpdateStatus(_ context.Context, id uuid.UUID, change domain.InstanceStatusChange) (string, error) {
	instance, found := s.instances[id]
	if !found {
		return "", domain.InstanceNotFound()
	}
	previous := instance.Status
	instance.Status = change.Status
	instance.StatusReason = change.Reason
	return previous, nil
}

func (s *fakeStore) UpdateIdentity(_ context.Context, id uuid.UUID, identity domain.InstanceIdentity) error {
	instance, found := s.instances[id]
	if !found {
		return domain.InstanceNotFound()
	}
	instance.JID = identity.JID
	instance.Phone = identity.Phone
	instance.LID = identity.LID
	return nil
}

func (s *fakeStore) SetAutoConnect(_ context.Context, id uuid.UUID, autoConnect bool) error {
	s.instances[id].AutoConnect = autoConnect
	return nil
}

func (s *fakeStore) SoftDelete(_ context.Context, _ string, id uuid.UUID) error {
	now := time.Now()
	s.instances[id].DeletedAt = &now
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *fakeStore) Insert(_ context.Context, event *domain.OutboxEvent) error {
	s.outbox = append(s.outbox, *event)
	return nil
}

func (s *fakeStore) Do(_ context.Context, run func(unit Unit) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doErr != nil {
		return s.doErr
	}
	return run(Unit{Instances: s, Outbox: s})
}

func (s *fakeStore) snapshot(id uuid.UUID) domain.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.instances[id]
}

func (s *fakeStore) outboxCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.outbox)
}

func (s *fakeStore) lastOutbox() domain.OutboxEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outbox[len(s.outbox)-1]
}

type fakeClient struct {
	mu            sync.Mutex
	connected     bool
	connectErr    error
	logoutErr     error
	pairErr       error
	deleteErr     error
	qrItems       chan QRItem
	calls         []string
	released      bool
	deviceDeleted bool
	emit          func(Event)
	qrCtx         context.Context
}

func (c *fakeClient) record(call string) {
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
}

func (c *fakeClient) called(call string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, recorded := range c.calls {
		if recorded == call {
			return true
		}
	}
	return false
}

func (c *fakeClient) Connect() error {
	c.record("connect")
	if c.connectErr != nil {
		return c.connectErr
	}
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return nil
}

func (c *fakeClient) Disconnect() {
	c.record("disconnect")
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
}

func (c *fakeClient) Logout(_ context.Context) error {
	c.record("logout")
	return c.logoutErr
}

func (c *fakeClient) DeleteDevice(_ context.Context) error {
	c.record("delete_device")
	c.mu.Lock()
	c.deviceDeleted = true
	c.mu.Unlock()
	return c.deleteErr
}

func (c *fakeClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *fakeClient) OpenQRChannel(ctx context.Context) (<-chan QRItem, error) {
	c.record("open_qr")
	c.mu.Lock()
	c.qrCtx = ctx
	c.mu.Unlock()
	return c.qrItems, nil
}

func (c *fakeClient) PairPhone(_ context.Context, phone string) (string, error) {
	c.record("pair_phone:" + phone)
	if c.pairErr != nil {
		return "", c.pairErr
	}
	return "ABCD-EFGH", nil
}

func (c *fakeClient) Release() {
	c.record("release")
	c.mu.Lock()
	c.released = true
	c.mu.Unlock()
}

type fakeFactory struct {
	mu        sync.Mutex
	clients   []*fakeClient
	createErr error
	prepare   func(*fakeClient)
}

func (f *fakeFactory) Create(_ context.Context, _ domain.Instance, emit func(Event)) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	created := &fakeClient{qrItems: make(chan QRItem, 8), emit: emit}
	if f.prepare != nil {
		f.prepare(created)
	}
	f.clients = append(f.clients, created)
	return created, nil
}

func (f *fakeFactory) last() *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clients[len(f.clients)-1]
}

func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

var errBoom = errors.New("boom")

func pairedInstance(status string) domain.Instance {
	jid := "5511999999999:1@s.whatsapp.net"
	phone := "5511999999999"
	return domain.Instance{
		ID:          uuid.New(),
		ClientID:    "godi",
		Name:        "main",
		Status:      status,
		AutoConnect: true,
		JID:         &jid,
		Phone:       &phone,
	}
}

func unpairedInstance() domain.Instance {
	return domain.Instance{
		ID:          uuid.New(),
		ClientID:    "godi",
		Name:        "new",
		Status:      domain.InstanceStatusCreated,
		AutoConnect: true,
	}
}

type harness struct {
	manager *Manager
	store   *fakeStore
	factory *fakeFactory
}

func newHarness(t *testing.T, instance domain.Instance) *harness {
	t.Helper()
	store := newFakeStore(instance)
	factory := &fakeFactory{}
	manager := NewManager(Options{
		Instances:        store,
		Unit:             store,
		Clients:          factory,
		WatchdogInterval: 10 * time.Millisecond,
		QRWait:           time.Second,
	})
	t.Cleanup(manager.Stop)
	return &harness{manager: manager, store: store, factory: factory}
}

func (h *harness) feed(t *testing.T, id uuid.UUID, event Event) {
	t.Helper()
	current, err := h.manager.entryFor(id)
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	current.mu.Lock()
	generation := current.generation.Load()
	current.mu.Unlock()
	current.emitter(generation)(event)
}

func (h *harness) waitStatus(t *testing.T, id uuid.UUID, status string) domain.Instance {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if instance := h.store.snapshot(id); instance.Status == status {
			return instance
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status did not reach %s, got %s", status, h.store.snapshot(id).Status)
	return domain.Instance{}
}

func (c *fakeClient) qrContext() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.qrCtx
}

func (c *fakeClient) callCount(call string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, recorded := range c.calls {
		if recorded == call {
			total++
		}
	}
	return total
}

func (s *fakeStore) setAutoConnect(id uuid.UUID, autoConnect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[id].AutoConnect = autoConnect
}
