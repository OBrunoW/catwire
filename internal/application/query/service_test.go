package query

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	instances map[uuid.UUID]string
	messages  []domain.Message
	chats     []domain.ChatView
	media     []domain.Media
	lastMsg   domain.MessageFilter
	lastChat  domain.ChatFilter
}

func (f *fakeStore) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	if owner, found := f.instances[id]; !found || owner != clientID {
		return nil, domain.InstanceNotFound()
	}
	return &domain.Instance{ID: id, ClientID: clientID}, nil
}

type fakeMessages struct{ store *fakeStore }

func (f fakeMessages) List(_ context.Context, instanceID uuid.UUID, filter domain.MessageFilter, params apihttp.PageParams) (apihttp.PageResult[domain.Message], error) {
	f.store.lastMsg = filter
	items := []domain.Message{}
	for _, message := range f.store.messages {
		if message.InstanceID == instanceID {
			items = append(items, message)
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Timestamp.After(items[b].Timestamp) })
	return apihttp.BuildPage(items, params.Limit, func(last domain.Message) string { return last.ID.String() }), nil
}

func (f fakeMessages) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.Message, error) {
	for _, message := range f.store.messages {
		if message.InstanceID == instanceID && message.ID == id {
			copied := message
			return &copied, nil
		}
	}
	return nil, domain.MessageNotFound()
}

func (f fakeMessages) FindReplyTargets(_ context.Context, instanceID uuid.UUID, references []domain.MessageRef) (map[domain.MessageRef]uuid.UUID, error) {
	targets := map[domain.MessageRef]uuid.UUID{}
	for _, reference := range references {
		for _, message := range f.store.messages {
			if message.InstanceID == instanceID && message.ChatID == reference.ChatID && message.WAMessageID == reference.WAMessageID {
				targets[reference] = message.ID
			}
		}
	}
	return targets, nil
}

type fakeChats struct{ store *fakeStore }

func (f fakeChats) ListByInstance(_ context.Context, instanceID uuid.UUID, filter domain.ChatFilter, params apihttp.PageParams) (apihttp.PageResult[domain.ChatView], error) {
	f.store.lastChat = filter
	items := []domain.ChatView{}
	for _, view := range f.store.chats {
		if view.InstanceID == instanceID {
			items = append(items, view)
		}
	}
	return apihttp.BuildPage(items, params.Limit, func(last domain.ChatView) string { return last.ID.String() }), nil
}

func (f fakeChats) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error) {
	for _, view := range f.store.chats {
		if view.InstanceID == instanceID && view.ID == id {
			copied := view
			return &copied, nil
		}
	}
	return nil, domain.ChatNotFound()
}

func (f fakeChats) FindJIDsByIDs(_ context.Context, instanceID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	jids := map[uuid.UUID]string{}
	for _, view := range f.store.chats {
		for _, id := range ids {
			if view.InstanceID == instanceID && view.ID == id {
				jids[id] = view.JID
			}
		}
	}
	return jids, nil
}

type fakeMedia struct{ store *fakeStore }

func (f fakeMedia) FindByMessageIDs(_ context.Context, ids []uuid.UUID) ([]domain.Media, error) {
	found := []domain.Media{}
	for _, item := range f.store.media {
		for _, id := range ids {
			if item.MessageID != nil && *item.MessageID == id {
				found = append(found, item)
			}
		}
	}
	return found, nil
}

type fixture struct {
	service    *Service
	store      *fakeStore
	instanceID uuid.UUID
	chat       domain.ChatView
}

func newFixture(baseURL string) fixture {
	instanceID := uuid.New()
	store := &fakeStore{instances: map[uuid.UUID]string{instanceID: "client-a"}}
	chat := domain.ChatView{Chat: domain.Chat{ID: uuid.New(), InstanceID: instanceID, JID: "5511988887777@s.whatsapp.net", Type: domain.ChatTypeDirect}}
	store.chats = append(store.chats, chat)
	signer, _ := media.NewSigner("secret", func() time.Time { return fixedNow })
	service := NewService(Options{
		Instances: store,
		Messages:  fakeMessages{store: store},
		Chats:     fakeChats{store: store},
		Media:     fakeMedia{store: store},
		URLs:      media.NewURLBuilder(signer, baseURL, time.Hour),
	})
	return fixture{service: service, store: store, instanceID: instanceID, chat: chat}
}

func (f *fixture) addMessage(offset time.Duration) domain.Message {
	message := domain.Message{
		ID:          uuid.New(),
		InstanceID:  f.instanceID,
		ChatID:      f.chat.ID,
		WAMessageID: uuid.NewString(),
		Direction:   domain.DirectionInbound,
		Type:        "text",
		Timestamp:   fixedNow.Add(offset),
	}
	f.store.messages = append(f.store.messages, message)
	return message
}

func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Status != status || appErr.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

func TestListMessagesPaginatesAndForwardsFilter(t *testing.T) {
	f := newFixture("")
	f.addMessage(-time.Minute)
	newest := f.addMessage(0)
	f.addMessage(-2 * time.Minute)

	page, err := f.service.ListMessages(context.Background(), "client-a", f.instanceID,
		domain.MessageFilter{Direction: domain.DirectionInbound, Search: "nota"}, apihttp.PageParams{Limit: 2})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Data) != 2 || !page.HasMore || page.NextCursor == nil || page.Data[0].ID != newest.ID {
		t.Fatalf("unexpected page %+v", page)
	}
	if page.Data[0].ChatJID != f.chat.JID {
		t.Fatalf("chat jid not resolved: %q", page.Data[0].ChatJID)
	}
	if f.store.lastMsg.Direction != domain.DirectionInbound || f.store.lastMsg.Search != "nota" {
		t.Fatalf("filter not forwarded: %+v", f.store.lastMsg)
	}
}

func TestListMessagesRejectsInvalidFilters(t *testing.T) {
	f := newFixture("")
	later := fixedNow.Add(time.Hour)
	cases := map[string]domain.MessageFilter{
		"direction": {Direction: "sideways"},
		"source":    {Source: "cloud"},
		"type":      {Type: "hologram"},
		"range":     {Since: &later, Until: &fixedNow},
	}
	for label, filter := range cases {
		_, err := f.service.ListMessages(context.Background(), "client-a", f.instanceID, filter, apihttp.PageParams{Limit: 10})
		if appErr, ok := domain.AsAppError(err); !ok || appErr.Status != 400 {
			t.Fatalf("%s: expected 400, got %v", label, err)
		}
	}
}

func TestListMessagesOfAnotherClientIsNotFound(t *testing.T) {
	f := newFixture("")
	f.addMessage(0)

	_, err := f.service.ListMessages(context.Background(), "client-b", f.instanceID, domain.MessageFilter{}, apihttp.PageParams{Limit: 10})

	assertCode(t, err, 404, "INSTANCE_NOT_FOUND")
}

func TestGetMessageBuildsMediaWithSignedURLAndReplyTarget(t *testing.T) {
	f := newFixture("https://catwire.example.com")
	original := f.addMessage(-time.Minute)
	reply := f.addMessage(0)
	for index := range f.store.messages {
		if f.store.messages[index].ID == reply.ID {
			f.store.messages[index].ReplyToWAMessageID = &original.WAMessageID
		}
	}
	mimeType := "image/jpeg"
	f.store.media = append(f.store.media, domain.Media{
		ID: uuid.New(), InstanceID: f.instanceID, MessageID: &reply.ID, Kind: "image",
		Status: domain.MediaStatusStored, MimeType: &mimeType,
	})

	resource, err := f.service.GetMessage(context.Background(), "client-a", f.instanceID, reply.ID)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource.Media == nil || resource.Media.DownloadURL == nil || resource.Media.DownloadURLExpiresAt == nil {
		t.Fatalf("expected signed media, got %+v", resource.Media)
	}
	if resource.ReplyTo == nil || resource.ReplyTo.MessageID == nil || *resource.ReplyTo.MessageID != original.ID {
		t.Fatalf("unexpected reply_to %+v", resource.ReplyTo)
	}
}

func TestGetMessageWithoutPublicBaseURLHasNoDownloadURL(t *testing.T) {
	f := newFixture("")
	message := f.addMessage(0)
	f.store.media = append(f.store.media, domain.Media{
		ID: uuid.New(), InstanceID: f.instanceID, MessageID: &message.ID, Kind: "image", Status: domain.MediaStatusStored,
	})

	resource, err := f.service.GetMessage(context.Background(), "client-a", f.instanceID, message.ID)

	if err != nil || resource.Media == nil || resource.Media.DownloadURL != nil {
		t.Fatalf("unexpected result %+v %v", resource.Media, err)
	}
}

func TestGetMessageFromOtherClientOrInstanceIsNotFound(t *testing.T) {
	f := newFixture("")
	message := f.addMessage(0)

	_, otherClient := f.service.GetMessage(context.Background(), "client-b", f.instanceID, message.ID)
	_, otherInstance := f.service.GetMessage(context.Background(), "client-a", uuid.New(), message.ID)
	_, missing := f.service.GetMessage(context.Background(), "client-a", f.instanceID, uuid.New())

	assertCode(t, otherClient, 404, "INSTANCE_NOT_FOUND")
	assertCode(t, otherInstance, 404, "INSTANCE_NOT_FOUND")
	assertCode(t, missing, 404, "MESSAGE_NOT_FOUND")
}

func TestListChatsMapsLastMessageAndPaginates(t *testing.T) {
	f := newFixture("")
	text := "oi"
	lastID := uuid.New()
	withLast := domain.ChatView{
		Chat:        domain.Chat{ID: uuid.New(), InstanceID: f.instanceID, JID: "5511@s.whatsapp.net", Type: domain.ChatTypeDirect},
		LastMessage: &domain.LastMessageSummary{ID: lastID, Type: "text", Text: &text, Direction: domain.DirectionInbound, Timestamp: fixedNow},
	}
	f.store.chats = append(f.store.chats, withLast)

	page, err := f.service.ListChats(context.Background(), "client-a", f.instanceID, domain.ChatFilter{Type: domain.ChatTypeDirect}, apihttp.PageParams{Limit: 1})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Data) != 1 || !page.HasMore || f.store.lastChat.Type != domain.ChatTypeDirect {
		t.Fatalf("unexpected page %+v", page)
	}
	chat, err := f.service.GetChat(context.Background(), "client-a", f.instanceID, withLast.ID)
	if err != nil || chat.LastMessage == nil || chat.LastMessage.ID != lastID {
		t.Fatalf("unexpected chat %+v %v", chat, err)
	}
}

func TestListChatsRejectsInvalidType(t *testing.T) {
	f := newFixture("")

	_, err := f.service.ListChats(context.Background(), "client-a", f.instanceID, domain.ChatFilter{Type: "channel"}, apihttp.PageParams{Limit: 10})

	assertCode(t, err, 400, "BAD_REQUEST")
}

func TestGetChatFromOtherClientIsNotFound(t *testing.T) {
	f := newFixture("")

	_, otherClient := f.service.GetChat(context.Background(), "client-b", f.instanceID, f.chat.ID)
	_, missing := f.service.GetChat(context.Background(), "client-a", f.instanceID, uuid.New())

	assertCode(t, otherClient, 404, "INSTANCE_NOT_FOUND")
	assertCode(t, missing, 404, "CHAT_NOT_FOUND")
}
