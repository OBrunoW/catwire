package operations

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type readFixture struct {
	service   *ReadService
	instances *fakeInstances
	chats     *fakeChats
	messages  *fakeMessages
	gateway   *fakeReadGateway
	readAt    time.Time
}

func newReadFixture() readFixture {
	instances := &fakeInstances{instance: newConnectedInstance()}
	chats := &fakeChats{chat: newDirectChat(instances.instance.ID)}
	messages := &fakeMessages{}
	gateway := &fakeReadGateway{}
	readAt := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	service := NewReadService(instances, chats, messages, gateway, discardLogger())
	service.now = func() time.Time { return readAt }
	return readFixture{service, instances, chats, messages, gateway, readAt}
}

func (f readFixture) addMessage(direction, status, waID, sender string) domain.Message {
	message := domain.Message{
		ID: uuid.New(), InstanceID: f.instances.instance.ID, ChatID: f.chats.chat.ID,
		WAMessageID: waID, Direction: direction, Status: status, SenderJID: sender,
	}
	f.messages.messages = append(f.messages.messages, message)
	return message
}

func (f readFixture) mark(ids []uuid.UUID) (ReadResult, error) {
	return f.service.Mark(context.Background(), testClientID, f.instances.instance.ID, f.chats.chat.ID, ids)
}

func TestReadWithoutIDsMarksAllInboundReceived(t *testing.T) {
	fixture := newReadFixture()
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "A", "contact")
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "B", "contact")
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusRead, "C", "contact")
	fixture.addMessage(domain.DirectionOutbound, domain.MessageStatusSent, "D", "me")

	result, err := fixture.mark(nil)

	if err != nil || result.Marked != 2 || result.ChatID != fixture.chats.chat.ID {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
	receipt := fixture.gateway.receipts[0]
	if len(fixture.gateway.receipts) != 1 || len(receipt.WAMessageIDs) != 2 || receipt.ChatJID != fixture.chats.chat.JID ||
		receipt.SenderJID != fixture.chats.chat.JID || !receipt.ReadAt.Equal(fixture.readAt) {
		t.Fatalf("unexpected receipts: %+v", fixture.gateway.receipts)
	}
	if len(fixture.messages.advanced) != 2 {
		t.Fatalf("expected 2 advanced messages, got %d", len(fixture.messages.advanced))
	}
}

func TestReadWithIDsMarksOnlyThose(t *testing.T) {
	fixture := newReadFixture()
	first := fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "A", "contact")
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "B", "contact")

	result, err := fixture.mark([]uuid.UUID{first.ID})

	if err != nil || result.Marked != 1 {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
	if ids := fixture.gateway.receipts[0].WAMessageIDs; len(ids) != 1 || ids[0] != "A" {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestReadWithUnknownIDIsNotFound(t *testing.T) {
	fixture := newReadFixture()
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "A", "contact")

	_, err := fixture.mark([]uuid.UUID{uuid.New()})

	requireCode(t, err, http.StatusNotFound, "MESSAGE_NOT_FOUND")
	if len(fixture.gateway.receipts) != 0 {
		t.Fatal("gateway must not be called")
	}
}

func TestReadWithOutboundIDIsBadRequest(t *testing.T) {
	fixture := newReadFixture()
	outbound := fixture.addMessage(domain.DirectionOutbound, domain.MessageStatusSent, "A", "me")

	_, err := fixture.mark([]uuid.UUID{outbound.ID})

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
}

func TestReadGroupSendsOneReceiptPerSender(t *testing.T) {
	fixture := newReadFixture()
	fixture.chats.chat.Type = domain.ChatTypeGroup
	fixture.chats.chat.JID = "120363000000000000@g.us"
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "A", "5511911111111@s.whatsapp.net")
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "B", "5511922222222@s.whatsapp.net")
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "C", "5511911111111@s.whatsapp.net")

	result, err := fixture.mark(nil)

	if err != nil || result.Marked != 3 || len(fixture.gateway.receipts) != 2 {
		t.Fatalf("unexpected result: %+v receipts=%d err=%v", result, len(fixture.gateway.receipts), err)
	}
	if first := fixture.gateway.receipts[0]; first.SenderJID != "5511911111111@s.whatsapp.net" || len(first.WAMessageIDs) != 2 {
		t.Fatalf("unexpected first receipt: %+v", first)
	}
}

func TestReadSplitsLargeBatches(t *testing.T) {
	fixture := newReadFixture()
	for index := 0; index < readBatchSize+1; index++ {
		fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, uuid.NewString(), "contact")
	}

	result, err := fixture.mark(nil)

	if err != nil || result.Marked != readBatchSize+1 || len(fixture.gateway.receipts) != 2 {
		t.Fatalf("unexpected result: %+v receipts=%d err=%v", result, len(fixture.gateway.receipts), err)
	}
}

func TestReadWithNothingPendingMarksZero(t *testing.T) {
	fixture := newReadFixture()

	result, err := fixture.mark(nil)

	if err != nil || result.Marked != 0 || len(fixture.gateway.receipts) != 0 {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
}

func TestReadDisconnectedInstanceIsConflict(t *testing.T) {
	fixture := newReadFixture()
	fixture.instances.instance.Status = "disconnected"

	_, err := fixture.mark(nil)

	requireCode(t, err, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestReadGatewayFailureKeepsStatusUntouched(t *testing.T) {
	fixture := newReadFixture()
	fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "A", "contact")
	fixture.gateway.err = errWhatsApp

	result, err := fixture.mark(nil)

	requireCode(t, err, http.StatusBadGateway, "WHATSAPP_ERROR")
	if result.Marked != 0 || len(fixture.messages.advanced) != 0 {
		t.Fatalf("status advanced after failure: %+v", fixture.messages.advanced)
	}
}

func TestReadWithAlreadyReadIDsDoesNotCallGatewayNorCount(t *testing.T) {
	fixture := newReadFixture()
	read := fixture.addMessage(domain.DirectionInbound, domain.MessageStatusRead, "A", "contact")
	pending := fixture.addMessage(domain.DirectionInbound, domain.MessageStatusReceived, "B", "contact")

	result, err := fixture.mark([]uuid.UUID{read.ID})
	if err != nil || result.Marked != 0 || len(fixture.gateway.receipts) != 0 || len(fixture.messages.advanced) != 0 {
		t.Fatalf("unexpected result: %+v receipts=%d err=%v", result, len(fixture.gateway.receipts), err)
	}

	result, err = fixture.mark([]uuid.UUID{read.ID, pending.ID})
	if err != nil || result.Marked != 1 || len(fixture.gateway.receipts) != 1 {
		t.Fatalf("unexpected result: %+v receipts=%d err=%v", result, len(fixture.gateway.receipts), err)
	}
	if ids := fixture.gateway.receipts[0].WAMessageIDs; len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestReadWithTooManyIDsIsBadRequest(t *testing.T) {
	fixture := newReadFixture()
	ids := make([]uuid.UUID, maxMessageIDs+1)
	for index := range ids {
		ids[index] = uuid.New()
	}

	_, err := fixture.mark(ids)

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	if len(fixture.gateway.receipts) != 0 {
		t.Fatal("gateway must not be called")
	}
}
