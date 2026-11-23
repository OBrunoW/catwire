package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type fakeDB struct {
	mu        sync.Mutex
	media     []*domain.Media
	messages  []*domain.Message
	chats     []*domain.Chat
	instances []*domain.Instance
	outbox    []domain.OutboxEvent
	failures  map[uuid.UUID]time.Time
	retryPath map[uuid.UUID]string
}

func newFakeDB() *fakeDB {
	return &fakeDB{failures: map[uuid.UUID]time.Time{}, retryPath: map[uuid.UUID]string{}}
}

func (d *fakeDB) Do(_ context.Context, run func(unit Unit) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return run(Unit{Media: &fakeMedia{db: d}, Messages: &fakeMessages{db: d}, Chats: &fakeChats{db: d}, Instances: &fakeInstances{db: d}, Outbox: &fakeOutbox{db: d}})
}

func (d *fakeDB) seed() *domain.Media {
	instance := &domain.Instance{ID: uuid.New(), ClientID: "client-a"}
	chat := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: "5511999999999@s.whatsapp.net"}
	message := &domain.Message{ID: uuid.New(), InstanceID: instance.ID, ChatID: chat.ID, WAMessageID: "3EB0AA", SenderJID: chat.JID}
	directPath := "/v/t62/secret-path"
	size := int64(10)
	mimeType := "image/jpeg"
	item := &domain.Media{
		ID: uuid.New(), InstanceID: instance.ID, MessageID: &message.ID, Kind: "image", Status: domain.MediaStatusPending,
		MimeType: &mimeType, SizeBytes: &size, WADirectPath: &directPath,
		WAMediaKey: []byte("media-key"), WAFileSHA256: []byte("sha"), WAFileEncSHA256: []byte("enc"),
	}
	d.instances = append(d.instances, instance)
	d.chats = append(d.chats, chat)
	d.messages = append(d.messages, message)
	d.media = append(d.media, item)
	return item
}

func (d *fakeDB) eventTypes() []string {
	types := []string{}
	for _, event := range d.outbox {
		types = append(types, event.EventType)
	}
	return types
}

func (d *fakeDB) find(id uuid.UUID) *domain.Media {
	for _, item := range d.media {
		if item.ID == id {
			return item
		}
	}
	return nil
}

type fakeMedia struct{ db *fakeDB }

func (m *fakeMedia) ClaimPending(_ context.Context, limit int, _ time.Duration) ([]domain.Media, error) {
	claimed := []domain.Media{}
	for _, item := range m.db.media {
		if item.Status == domain.MediaStatusPending && len(claimed) < limit {
			claimed = append(claimed, *item)
		}
	}
	return claimed, nil
}

func (m *fakeMedia) MarkStored(_ context.Context, id uuid.UUID, stored domain.StoredMedia) error {
	item := m.db.find(id)
	item.Status = domain.MediaStatusStored
	item.StorageKey = &stored.StorageKey
	item.MimeType = &stored.MimeType
	item.SizeBytes = &stored.SizeBytes
	item.SHA256 = &stored.SHA256
	return nil
}

func (m *fakeMedia) Reschedule(_ context.Context, id uuid.UUID, next time.Time) error {
	item := m.db.find(id)
	item.NextAttemptAt = &next
	m.db.failures[id] = next
	return nil
}

func (m *fakeMedia) RecordDownloadFailure(_ context.Context, id uuid.UUID, message string, next *time.Time) error {
	item := m.db.find(id)
	item.DownloadAttempts++
	item.LastError = &message
	item.NextAttemptAt = next
	if next == nil {
		item.Status = domain.MediaStatusFailed
		return nil
	}
	m.db.failures[id] = *next
	return nil
}

func (m *fakeMedia) ApplyRetryPath(_ context.Context, id uuid.UUID, directPath string) error {
	item := m.db.find(id)
	item.WADirectPath = &directPath
	item.LastError = nil
	m.db.retryPath[id] = directPath
	return nil
}

func (m *fakeMedia) FindByMessageIDs(_ context.Context, ids []uuid.UUID) ([]domain.Media, error) {
	found := []domain.Media{}
	for _, item := range m.db.media {
		for _, id := range ids {
			if item.MessageID != nil && *item.MessageID == id {
				found = append(found, *item)
			}
		}
	}
	return found, nil
}

func (m *fakeMedia) ListExpiredUnattached(_ context.Context, limit int) ([]domain.Media, error) {
	expired := []domain.Media{}
	for _, item := range m.db.media {
		if item.MessageID == nil && item.ExpiresAt != nil && !item.ExpiresAt.After(fixedNow) && len(expired) < limit {
			expired = append(expired, *item)
		}
	}
	return expired, nil
}

func (m *fakeMedia) Delete(_ context.Context, id uuid.UUID) error {
	kept := m.db.media[:0]
	for _, item := range m.db.media {
		if item.ID != id {
			kept = append(kept, item)
		}
	}
	m.db.media = kept
	return nil
}

type fakeMessages struct{ db *fakeDB }

func (m *fakeMessages) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.Message, error) {
	for _, message := range m.db.messages {
		if message.ID == id && message.InstanceID == instanceID {
			return message, nil
		}
	}
	return nil, domain.MessageNotFound()
}

func (m *fakeMessages) FindByWAMessageID(_ context.Context, instanceID uuid.UUID, waMessageID string) (*domain.Message, error) {
	for _, message := range m.db.messages {
		if message.WAMessageID == waMessageID && message.InstanceID == instanceID {
			return message, nil
		}
	}
	return nil, domain.MessageNotFound()
}

type fakeChats struct{ db *fakeDB }

func (c *fakeChats) FindByID(_ context.Context, _, id uuid.UUID) (*domain.ChatView, error) {
	for _, chat := range c.db.chats {
		if chat.ID == id {
			return &domain.ChatView{Chat: *chat}, nil
		}
	}
	return nil, domain.ChatNotFound()
}

type fakeInstances struct{ db *fakeDB }

func (i *fakeInstances) FindUnscoped(_ context.Context, id uuid.UUID) (*domain.Instance, error) {
	for _, instance := range i.db.instances {
		if instance.ID == id {
			return instance, nil
		}
	}
	return nil, domain.InstanceNotFound()
}

type fakeOutbox struct{ db *fakeDB }

func (o *fakeOutbox) Insert(_ context.Context, event *domain.OutboxEvent) error {
	o.db.outbox = append(o.db.outbox, *event)
	return nil
}

type fakeStorage struct {
	objects map[string][]byte
	puts    []string
	deleted []string
	putErr  error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: map[string][]byte{}}
}

func (s *fakeStorage) Put(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	if s.putErr != nil {
		return s.putErr
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.objects[key] = content
	s.puts = append(s.puts, key)
	return nil
}

func (s *fakeStorage) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	content, found := s.objects[key]
	if !found {
		return nil, ErrObjectNotFound
	}
	return nopSeekCloser{Reader: bytesReader(content)}, nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeStorage) Stat(_ context.Context, key string) (ObjectInfo, error) {
	content, found := s.objects[key]
	if !found {
		return ObjectInfo{}, ErrObjectNotFound
	}
	return ObjectInfo{Size: int64(len(content))}, nil
}

type fakeDownloader struct {
	content  []byte
	err      error
	requests []DownloadRequest
}

func (d *fakeDownloader) Download(_ context.Context, request DownloadRequest, destination DownloadTarget) error {
	d.requests = append(d.requests, request)
	if d.err != nil {
		return d.err
	}
	_, err := destination.Write(d.content)
	return err
}

type fakeConverter struct {
	calls int
	err   error
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

type fakeRetries struct {
	targets []RetryTarget
	err     error
}

func (r *fakeRetries) RequestRetry(_ context.Context, _ uuid.UUID, target RetryTarget) error {
	r.targets = append(r.targets, target)
	return r.err
}

var errBoom = errors.New("boom")

type chunkedDownloader struct {
	chunks  int
	written int
}

func (d *chunkedDownloader) Download(_ context.Context, _ DownloadRequest, destination DownloadTarget) error {
	for index := 0; index < d.chunks; index++ {
		if _, err := destination.Write([]byte("0123456789")); err != nil {
			return err
		}
		d.written++
	}
	return nil
}

type captureHandler struct {
	mu      sync.Mutex
	records []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		line += " " + attr.Key + "=" + attr.Value.String()
		return true
	})
	h.records = append(h.records, line)
	return nil
}

func (h *captureHandler) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.records, "\n")
}
