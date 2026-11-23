package ingestion

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var statusRanks = map[string]int{
	domain.MessageStatusPending:   0,
	domain.MessageStatusReceived:  0,
	domain.MessageStatusSent:      1,
	domain.MessageStatusDelivered: 2,
	domain.MessageStatusRead:      3,
	domain.MessageStatusPlayed:    4,
}

type fakeDB struct {
	mu       sync.Mutex
	chats    []*domain.Chat
	messages []*domain.Message
	media    []*domain.Media
	outbox   []domain.OutboxEvent
	settings domain.InstanceSettings
}

func newFakeDB() *fakeDB {
	return &fakeDB{settings: domain.InstanceSettings{DownloadMedia: true}}
}

func (d *fakeDB) Do(_ context.Context, run func(unit Unit) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return run(Unit{
		Chats:    &fakeChats{db: d},
		Messages: &fakeMessages{db: d},
		Media:    &fakeMedia{db: d},
		Outbox:   &fakeOutbox{db: d},
	})
}

func (d *fakeDB) FindByInstanceID(_ context.Context, _ uuid.UUID) (*domain.InstanceSettings, error) {
	settings := d.settings
	return &settings, nil
}

func (d *fakeDB) eventTypes() []string {
	types := []string{}
	for _, event := range d.outbox {
		types = append(types, event.EventType)
	}
	return types
}

type fakeChats struct {
	db *fakeDB
}

func (c *fakeChats) FindByID(_ context.Context, _, id uuid.UUID) (*domain.ChatView, error) {
	for _, chat := range c.db.chats {
		if chat.ID == id {
			return &domain.ChatView{Chat: *chat}, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (c *fakeChats) FindByJID(_ context.Context, _ uuid.UUID, jid string) (*domain.Chat, error) {
	for _, chat := range c.db.chats {
		if chat.JID == jid {
			copied := *chat
			return &copied, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (c *fakeChats) FindByLID(_ context.Context, _ uuid.UUID, lid string) (*domain.Chat, error) {
	for _, chat := range c.db.chats {
		if chat.LID != nil && *chat.LID == lid {
			copied := *chat
			return &copied, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (c *fakeChats) Upsert(_ context.Context, chat *domain.Chat) error {
	for _, existing := range c.db.chats {
		if existing.JID != chat.JID {
			continue
		}
		if chat.LID != nil {
			existing.LID = chat.LID
		}
		*chat = *existing
		return nil
	}
	stored := *chat
	c.db.chats = append(c.db.chats, &stored)
	return nil
}

func (c *fakeChats) UpdateJID(_ context.Context, _, id uuid.UUID, jid string, phone *string) error {
	for _, chat := range c.db.chats {
		if chat.ID != id {
			continue
		}
		chat.JID = jid
		if phone != nil {
			chat.Phone = phone
		}
		return nil
	}
	return domain.ChatNotFound()
}

func (c *fakeChats) TouchLastMessage(_ context.Context, _, id, messageID uuid.UUID, at time.Time) error {
	for _, chat := range c.db.chats {
		if chat.ID == id {
			chat.LastMessageID = &messageID
			chat.LastMessageAt = &at
		}
	}
	return nil
}

type fakeMessages struct {
	db *fakeDB
}

func (m *fakeMessages) find(match func(*domain.Message) bool) (*domain.Message, error) {
	for _, message := range m.db.messages {
		if match(message) {
			copied := *message
			return &copied, nil
		}
	}
	return nil, domain.MessageNotFound()
}

func (m *fakeMessages) Insert(_ context.Context, message *domain.Message) (bool, error) {
	for _, existing := range m.db.messages {
		if existing.ChatID == message.ChatID && existing.WAMessageID == message.WAMessageID {
			return false, nil
		}
	}
	message.CreatedAt = time.Now()
	message.UpdatedAt = message.CreatedAt
	stored := *message
	m.db.messages = append(m.db.messages, &stored)
	return true, nil
}

func (m *fakeMessages) FindByID(_ context.Context, _, id uuid.UUID) (*domain.Message, error) {
	return m.find(func(message *domain.Message) bool { return message.ID == id })
}

func (m *fakeMessages) FindByWAMessageID(_ context.Context, _ uuid.UUID, waMessageID string) (*domain.Message, error) {
	return m.find(func(message *domain.Message) bool { return message.WAMessageID == waMessageID })
}

func (m *fakeMessages) FindByChatAndWAMessageID(
	_ context.Context,
	_, chatID uuid.UUID,
	waMessageID string,
) (*domain.Message, error) {
	return m.find(func(message *domain.Message) bool {
		return message.ChatID == chatID && message.WAMessageID == waMessageID
	})
}

func (m *fakeMessages) AdvanceStatusByWAMessageID(
	_ context.Context,
	_ uuid.UUID,
	waMessageID string,
	status string,
	_ time.Time,
) ([]domain.StatusAdvance, error) {
	advances := []domain.StatusAdvance{}
	for _, message := range m.db.messages {
		if message.WAMessageID != waMessageID || statusRanks[message.Status] >= statusRanks[status] {
			continue
		}
		advances = append(advances, domain.StatusAdvance{
			MessageID:      message.ID,
			ChatID:         message.ChatID,
			PreviousStatus: message.Status,
		})
		message.Status = status
	}
	return advances, nil
}

func (m *fakeMessages) ReplaceUndecryptable(
	_ context.Context,
	_, id uuid.UUID,
	update domain.MessageContentUpdate,
) (bool, error) {
	for _, message := range m.db.messages {
		if message.ID == id && message.Type == MessageTypeUndecryptable {
			message.Type = update.Type
			message.Text = update.Text
			message.Content = update.Content
			return true, nil
		}
	}
	return false, nil
}

func (m *fakeMessages) ApplyEdit(
	_ context.Context,
	_, id uuid.UUID,
	text *string,
	content json.RawMessage,
	at time.Time,
) (bool, error) {
	for _, message := range m.db.messages {
		if message.ID == id && message.RevokedAt == nil {
			message.Text = text
			message.Content = content
			message.EditedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (m *fakeMessages) ApplyRevocation(_ context.Context, _, id uuid.UUID, at time.Time) (bool, error) {
	for _, message := range m.db.messages {
		if message.ID == id && message.RevokedAt == nil {
			message.RevokedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (m *fakeMessages) ApplyReaction(
	_ context.Context,
	_, id uuid.UUID,
	reaction domain.Reaction,
	removed bool,
) (bool, error) {
	for _, message := range m.db.messages {
		if message.ID != id {
			continue
		}
		encoded := []domain.Reaction{}
		if !removed {
			encoded = append(encoded, reaction)
		}
		raw, err := json.Marshal(encoded)
		if err != nil {
			return false, err
		}
		message.Reactions = raw
		return true, nil
	}
	return false, nil
}

type fakeMedia struct {
	db *fakeDB
}

func (m *fakeMedia) Insert(_ context.Context, media *domain.Media) error {
	stored := *media
	m.db.media = append(m.db.media, &stored)
	return nil
}

func (m *fakeMedia) FindByMessageIDs(_ context.Context, messageIDs []uuid.UUID) ([]domain.Media, error) {
	found := []domain.Media{}
	for _, media := range m.db.media {
		for _, id := range messageIDs {
			if media.MessageID != nil && *media.MessageID == id {
				found = append(found, *media)
			}
		}
	}
	return found, nil
}

type fakeOutbox struct {
	db *fakeDB
}

func (o *fakeOutbox) Insert(_ context.Context, event *domain.OutboxEvent) error {
	o.db.outbox = append(o.db.outbox, *event)
	return nil
}
