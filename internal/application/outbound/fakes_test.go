package outbound

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

type fakeDB struct {
	mu        sync.Mutex
	instances map[uuid.UUID]*domain.Instance
	chats     []*domain.Chat
	messages  []*domain.Message
	media     []*domain.Media
	outbox    []domain.OutboxEvent
	clock     time.Time
}

func newFakeDB(clock time.Time) *fakeDB {
	return &fakeDB{instances: map[uuid.UUID]*domain.Instance{}, clock: clock}
}

func (d *fakeDB) addInstance(status string) *domain.Instance {
	jid := "5511900000000@s.whatsapp.net"
	instance := &domain.Instance{ID: uuid.New(), ClientID: "client", Status: status, JID: &jid}
	d.instances[instance.ID] = instance
	return instance
}

func (d *fakeDB) Do(_ context.Context, run func(unit Unit) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return run(Unit{
		Instances: &fakeInstances{db: d},
		Chats:     &fakeChats{db: d},
		Messages:  &fakeMessages{db: d},
		Media:     &fakeMedia{db: d},
		Outbox:    &fakeOutbox{db: d},
	})
}

func (d *fakeDB) eventTypes() []string {
	types := []string{}
	for _, event := range d.outbox {
		types = append(types, event.EventType)
	}
	return types
}

func (d *fakeDB) onlyMessage() *domain.Message {
	return d.messages[0]
}

type fakeInstances struct{ db *fakeDB }

func (f *fakeInstances) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	instance, found := f.db.instances[id]
	if !found || instance.ClientID != clientID {
		return nil, domain.InstanceNotFound()
	}
	copied := *instance
	return &copied, nil
}

func (f *fakeInstances) FindUnscoped(_ context.Context, id uuid.UUID) (*domain.Instance, error) {
	instance, found := f.db.instances[id]
	if !found {
		return nil, domain.InstanceNotFound()
	}
	copied := *instance
	return &copied, nil
}

type fakeChats struct{ db *fakeDB }

func (f *fakeChats) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error) {
	for _, chat := range f.db.chats {
		if chat.ID == id && chat.InstanceID == instanceID {
			return &domain.ChatView{Chat: *chat}, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (f *fakeChats) FindByJID(_ context.Context, instanceID uuid.UUID, jid string) (*domain.Chat, error) {
	for _, chat := range f.db.chats {
		if chat.JID == jid && chat.InstanceID == instanceID {
			copied := *chat
			return &copied, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (f *fakeChats) FindByLID(_ context.Context, instanceID uuid.UUID, lid string) (*domain.Chat, error) {
	for _, chat := range f.db.chats {
		if chat.LID != nil && *chat.LID == lid && chat.InstanceID == instanceID {
			copied := *chat
			return &copied, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (f *fakeChats) Upsert(_ context.Context, chat *domain.Chat) error {
	stored := *chat
	f.db.chats = append(f.db.chats, &stored)
	return nil
}

func (f *fakeChats) TouchLastMessage(_ context.Context, _, id, messageID uuid.UUID, at time.Time) error {
	for _, chat := range f.db.chats {
		if chat.ID == id {
			chat.LastMessageID = &messageID
			chat.LastMessageAt = &at
		}
	}
	return nil
}

type fakeMessages struct{ db *fakeDB }

func (f *fakeMessages) Insert(_ context.Context, message *domain.Message) (bool, error) {
	for _, existing := range f.db.messages {
		sameKey := existing.IdempotencyKey != nil && message.IdempotencyKey != nil &&
			*existing.IdempotencyKey == *message.IdempotencyKey && existing.InstanceID == message.InstanceID
		if sameKey {
			return false, domain.ErrIdempotencyKeyTaken
		}
		if existing.InstanceID == message.InstanceID && existing.ChatID == message.ChatID && existing.WAMessageID == message.WAMessageID {
			return false, nil
		}
	}
	message.CreatedAt = f.db.clock
	message.UpdatedAt = f.db.clock
	f.db.clock = f.db.clock.Add(time.Millisecond)
	stored := *message
	f.db.messages = append(f.db.messages, &stored)
	return true, nil
}

func (f *fakeMessages) find(match func(*domain.Message) bool) (*domain.Message, error) {
	for _, message := range f.db.messages {
		if match(message) {
			copied := *message
			return &copied, nil
		}
	}
	return nil, domain.MessageNotFound()
}

func (f *fakeMessages) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.Message, error) {
	return f.find(func(m *domain.Message) bool { return m.ID == id && m.InstanceID == instanceID })
}

func (f *fakeMessages) FindByChatAndWAMessageID(_ context.Context, instanceID, chatID uuid.UUID, waMessageID string) (*domain.Message, error) {
	return f.find(func(m *domain.Message) bool {
		return m.InstanceID == instanceID && m.ChatID == chatID && m.WAMessageID == waMessageID
	})
}

func (f *fakeMessages) FindByIdempotencyKey(_ context.Context, instanceID uuid.UUID, key string) (*domain.Message, error) {
	return f.find(func(m *domain.Message) bool {
		return m.InstanceID == instanceID && m.IdempotencyKey != nil && *m.IdempotencyKey == key
	})
}

func (f *fakeMessages) AdvanceStatus(_ context.Context, instanceID, id uuid.UUID, status string, at time.Time) (domain.StatusAdvance, bool, error) {
	for _, message := range f.db.messages {
		if message.ID == id && message.InstanceID == instanceID && message.Status == domain.MessageStatusPending {
			message.Status = status
			message.SentAt = &at
			return domain.StatusAdvance{MessageID: id, ChatID: message.ChatID, PreviousStatus: domain.MessageStatusPending}, true, nil
		}
	}
	return domain.StatusAdvance{}, false, nil
}

func (f *fakeMessages) MarkFailed(_ context.Context, instanceID, id uuid.UUID, code, message string) (bool, error) {
	for _, existing := range f.db.messages {
		if existing.ID == id && existing.InstanceID == instanceID && existing.Status == domain.MessageStatusPending {
			existing.Status = domain.MessageStatusFailed
			existing.ErrorCode = &code
			existing.ErrorMessage = &message
			existing.NextAttemptAt = nil
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeMessages) ScheduleSendRetry(_ context.Context, instanceID, id uuid.UUID, nextAttemptAt time.Time) error {
	for _, message := range f.db.messages {
		if message.ID == id && message.InstanceID == instanceID && message.Status == domain.MessageStatusPending {
			message.SendAttempts++
			message.NextAttemptAt = &nextAttemptAt
			return nil
		}
	}
	return domain.MessageNotFound()
}

func (f *fakeMessages) ListPendingForSend(_ context.Context, instanceID uuid.UUID, limit int) ([]domain.Message, error) {
	pending := []domain.Message{}
	for _, message := range f.db.messages {
		if message.InstanceID == instanceID && message.Status == domain.MessageStatusPending && message.Direction == domain.DirectionOutbound {
			pending = append(pending, *message)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	if len(pending) > 1 {
		pending = pending[:1]
	}
	if len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

func (f *fakeMessages) ListInstancesWithPendingSend(_ context.Context) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	ids := []uuid.UUID{}
	for _, message := range f.db.messages {
		if message.Status == domain.MessageStatusPending && message.Direction == domain.DirectionOutbound && !seen[message.InstanceID] {
			seen[message.InstanceID] = true
			ids = append(ids, message.InstanceID)
		}
	}
	return ids, nil
}

type fakeMedia struct{ db *fakeDB }

func (f *fakeMedia) Insert(_ context.Context, item *domain.Media) error {
	stored := *item
	f.db.media = append(f.db.media, &stored)
	return nil
}

func (f *fakeMedia) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Media, error) {
	for _, item := range f.db.media {
		owner, found := f.db.instances[item.InstanceID]
		if item.ID == id && found && owner.ClientID == clientID {
			copied := *item
			return &copied, nil
		}
	}
	return nil, domain.MediaNotFound()
}

func (f *fakeMedia) FindByMessageIDs(_ context.Context, messageIDs []uuid.UUID) ([]domain.Media, error) {
	items := []domain.Media{}
	for _, item := range f.db.media {
		for _, id := range messageIDs {
			if item.MessageID != nil && *item.MessageID == id {
				items = append(items, *item)
			}
		}
	}
	return items, nil
}

func (f *fakeMedia) Attach(_ context.Context, instanceID, id, messageID uuid.UUID) error {
	for _, item := range f.db.media {
		if item.ID == id && item.InstanceID == instanceID && item.MessageID == nil && item.Status == domain.MediaStatusStored {
			item.MessageID = &messageID
			item.ExpiresAt = nil
			return nil
		}
	}
	return domain.MediaNotFound()
}

type fakeOutbox struct{ db *fakeDB }

func (f *fakeOutbox) Insert(_ context.Context, event *domain.OutboxEvent) error {
	f.db.outbox = append(f.db.outbox, *event)
	return nil
}

type fakeGateway struct {
	mu            sync.Mutex
	registrations map[string]Registration
	lookups       []string
	lookupBarrier *barrier
	lookupErr     error
	sendErrors    []error
	sent          []SendCommand
	sentBodies    [][]byte
	afterSend     func()
	nextID        int
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{registrations: map[string]Registration{}}
}

func (g *fakeGateway) register(phone string) {
	g.registrations[phone] = Registration{Exists: true, JID: phone + "@s.whatsapp.net"}
}

func (g *fakeGateway) GenerateMessageID(_ uuid.UUID) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	return "3EB0" + strings.Repeat("A", 5) + string(rune('0'+g.nextID%10)) + uuid.NewString()[:4], nil
}

func (g *fakeGateway) IsOnWhatsApp(_ context.Context, _ uuid.UUID, phone string) (Registration, error) {
	g.mu.Lock()
	g.lookups = append(g.lookups, phone)
	registration := g.registrations[phone]
	err := g.lookupErr
	barrier := g.lookupBarrier
	g.mu.Unlock()
	if barrier != nil {
		barrier.wait()
	}
	return registration, err
}

func (g *fakeGateway) Send(_ context.Context, command SendCommand) (SendReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sent = append(g.sent, command)
	if command.Media != nil {
		body, _ := io.ReadAll(command.Media.Body)
		g.sentBodies = append(g.sentBodies, body)
	}
	if len(g.sendErrors) > 0 {
		err := g.sendErrors[0]
		g.sendErrors = g.sendErrors[1:]
		if err != nil {
			return SendReceipt{}, err
		}
	}
	if g.afterSend != nil {
		g.afterSend()
	}
	return SendReceipt{Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}, nil
}

func (g *fakeGateway) sendCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sent)
}

type barrier struct {
	mu      sync.Mutex
	count   int
	waiting int
	release chan struct{}
}

func newBarrier(count int) *barrier {
	return &barrier{count: count, release: make(chan struct{})}
}

func (b *barrier) wait() {
	b.mu.Lock()
	b.waiting++
	if b.waiting == b.count {
		close(b.release)
	}
	b.mu.Unlock()
	<-b.release
}

type fakeFetcher struct {
	content     []byte
	contentType string
	err         error
	calls       int
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (FetchedFile, error) {
	f.calls++
	if f.err != nil {
		return FetchedFile{}, f.err
	}
	return FetchedFile{Body: io.NopCloser(bytes.NewReader(f.content)), ContentType: f.contentType, FileName: "file.bin"}, nil
}

type fakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: map[string][]byte{}}
}

func (s *fakeStorage) Put(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = content
	return nil
}

func (s *fakeStorage) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.objects[key]
	if !found {
		return nil, media.ErrObjectNotFound
	}
	return nopSeekCloser{bytes.NewReader(content)}, nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *fakeStorage) Stat(_ context.Context, key string) (media.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.objects[key]
	if !found {
		return media.ObjectInfo{}, media.ErrObjectNotFound
	}
	return media.ObjectInfo{Size: int64(len(content))}, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

type fakeConverter struct {
	err   error
	calls int
}

func (c *fakeConverter) ToOggOpus(_ context.Context, inputPath, outputPath string) error {
	c.calls++
	if c.err != nil {
		return c.err
	}
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, append([]byte("OggS"), content...), 0o600)
}

var errBoom = errors.New("boom")
