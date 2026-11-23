package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const testClientID = "client"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeInstances struct {
	instance *domain.Instance
}

func newConnectedInstance() *domain.Instance {
	jid := "5511900000000@s.whatsapp.net"
	return &domain.Instance{ID: uuid.New(), ClientID: testClientID, Status: domain.InstanceStatusConnected, JID: &jid}
}

func (f *fakeInstances) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	if f.instance == nil || f.instance.ID != id || f.instance.ClientID != clientID {
		return nil, domain.InstanceNotFound()
	}
	copied := *f.instance
	return &copied, nil
}

type fakeChats struct {
	chat *domain.Chat
}

func (f *fakeChats) FindByID(_ context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error) {
	if f.chat == nil || f.chat.ID != id || f.chat.InstanceID != instanceID {
		return nil, domain.ChatNotFound()
	}
	return &domain.ChatView{Chat: *f.chat}, nil
}

func newDirectChat(instanceID uuid.UUID) *domain.Chat {
	return &domain.Chat{ID: uuid.New(), InstanceID: instanceID, JID: "5511988887777@s.whatsapp.net", Type: domain.ChatTypeDirect}
}

type fakeProfileGateway struct {
	data        ProfileData
	err         error
	statusCalls []string
	pushCalls   []string
}

func (f *fakeProfileGateway) GetProfile(context.Context, uuid.UUID) (ProfileData, error) {
	return f.data, f.err
}

func (f *fakeProfileGateway) SetStatusMessage(_ context.Context, _ uuid.UUID, text string) error {
	f.statusCalls = append(f.statusCalls, text)
	return f.err
}

func (f *fakeProfileGateway) SetPushName(_ context.Context, _ uuid.UUID, name string) error {
	f.pushCalls = append(f.pushCalls, name)
	return f.err
}

type fakePrivacyGateway struct {
	settings PrivacySettings
	err      error
	changes  []PrivacyChange
}

func (f *fakePrivacyGateway) GetPrivacy(context.Context, uuid.UUID) (PrivacySettings, error) {
	return f.settings, f.err
}

func (f *fakePrivacyGateway) SetPrivacy(_ context.Context, _ uuid.UUID, change PrivacyChange) (PrivacySettings, error) {
	f.changes = append(f.changes, change)
	if f.err != nil {
		return PrivacySettings{}, f.err
	}
	f.settings.ReadReceipts = change.Value
	return f.settings, nil
}

type fakePresenceGateway struct {
	err    error
	states []string
	chats  []string
}

func (f *fakePresenceGateway) SendPresence(_ context.Context, _ uuid.UUID, chatJID, state string) error {
	f.chats = append(f.chats, chatJID)
	f.states = append(f.states, state)
	return f.err
}

type fakeReadGateway struct {
	err      error
	receipts []ReadReceipt
}

func (f *fakeReadGateway) MarkRead(_ context.Context, _ uuid.UUID, receipt ReadReceipt) error {
	if f.err != nil {
		return f.err
	}
	f.receipts = append(f.receipts, receipt)
	return nil
}

type fakeMessages struct {
	messages []domain.Message
	advanced []uuid.UUID
}

func (f *fakeMessages) ListInboundReceived(context.Context, uuid.UUID, uuid.UUID) ([]domain.Message, error) {
	found := []domain.Message{}
	for _, message := range f.messages {
		if message.Direction == domain.DirectionInbound && message.Status == domain.MessageStatusReceived {
			found = append(found, message)
		}
	}
	return found, nil
}

func (f *fakeMessages) FindByChatAndIDs(_ context.Context, _, _ uuid.UUID, ids []uuid.UUID) ([]domain.Message, error) {
	found := []domain.Message{}
	for _, message := range f.messages {
		for _, id := range ids {
			if message.ID == id {
				found = append(found, message)
			}
		}
	}
	return found, nil
}

func (f *fakeMessages) AdvanceStatus(_ context.Context, _, id uuid.UUID, _ string, _ time.Time) (domain.StatusAdvance, bool, error) {
	f.advanced = append(f.advanced, id)
	return domain.StatusAdvance{MessageID: id}, true, nil
}

func (f *fakeMessages) FindOldestInChat(context.Context, uuid.UUID, uuid.UUID) (*domain.Message, error) {
	if len(f.messages) == 0 {
		return nil, domain.MessageNotFound()
	}
	oldest := f.messages[0]
	return &oldest, nil
}

type fakeHistoryGateway struct {
	err      error
	requests []HistoryRequest
}

func (f *fakeHistoryGateway) RequestHistory(_ context.Context, _ uuid.UUID, request HistoryRequest) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.requests = append(f.requests, request)
	return "3EB0REQUEST", nil
}

var errWhatsApp = errors.New("whatsapp refused")

func requireCode(t testingT, err error, status int, code string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Code != code || appErr.Status != status {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}
