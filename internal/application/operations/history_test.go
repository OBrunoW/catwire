package operations

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type historyFixture struct {
	service   *HistoryService
	instances *fakeInstances
	chats     *fakeChats
	messages  *fakeMessages
	gateway   *fakeHistoryGateway
}

func newHistoryFixture() historyFixture {
	instances := &fakeInstances{instance: newConnectedInstance()}
	chats := &fakeChats{chat: newDirectChat(instances.instance.ID)}
	messages := &fakeMessages{}
	gateway := &fakeHistoryGateway{}
	return historyFixture{NewHistoryService(instances, chats, messages, gateway, discardLogger()), instances, chats, messages, gateway}
}

func (f historyFixture) request(count int) (HistoryResult, error) {
	return f.service.Request(context.Background(), testClientID, f.instances.instance.ID, f.chats.chat.ID, count)
}

func TestHistoryRequestUsesOldestMessageAsAnchor(t *testing.T) {
	fixture := newHistoryFixture()
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fixture.messages.messages = []domain.Message{{
		ID: uuid.New(), WAMessageID: "OLDEST", Direction: domain.DirectionOutbound, Timestamp: timestamp,
	}}

	result, err := fixture.request(30)

	if err != nil || result.RequestID != "3EB0REQUEST" || result.ChatID != fixture.chats.chat.ID {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
	request := fixture.gateway.requests[0]
	if request.OldestWAMessageID != "OLDEST" || !request.OldestFromMe || !request.OldestTimestamp.Equal(timestamp) ||
		request.Count != 30 || request.ChatJID != fixture.chats.chat.JID {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestHistoryCountOutsideRangeIsBadRequest(t *testing.T) {
	fixture := newHistoryFixture()

	for _, count := range []int{0, -1, 51} {
		_, err := fixture.request(count)
		requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	}
	if len(fixture.gateway.requests) != 0 {
		t.Fatal("gateway must not be called")
	}
}

func TestHistoryCountBoundsAreAccepted(t *testing.T) {
	fixture := newHistoryFixture()
	fixture.messages.messages = []domain.Message{{ID: uuid.New(), WAMessageID: "A", Direction: domain.DirectionInbound}}

	for _, count := range []int{1, 50} {
		if _, err := fixture.request(count); err != nil {
			t.Fatalf("count %d: %v", count, err)
		}
	}
}

func TestHistoryWithoutAnchorIsUnavailable(t *testing.T) {
	fixture := newHistoryFixture()

	_, err := fixture.request(10)

	requireCode(t, err, http.StatusUnprocessableEntity, "HISTORY_UNAVAILABLE")
}

func TestHistoryDisconnectedInstanceIsConflict(t *testing.T) {
	fixture := newHistoryFixture()
	fixture.instances.instance.Status = "disconnected"

	_, err := fixture.request(10)

	requireCode(t, err, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestHistoryGatewayFailureMapsToBadGateway(t *testing.T) {
	fixture := newHistoryFixture()
	fixture.messages.messages = []domain.Message{{ID: uuid.New(), WAMessageID: "A", Direction: domain.DirectionInbound}}
	fixture.gateway.err = errWhatsApp

	_, err := fixture.request(10)

	requireCode(t, err, http.StatusBadGateway, "WHATSAPP_ERROR")
}
