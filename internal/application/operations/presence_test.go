package operations

import (
	"context"
	"net/http"
	"testing"
)

func newPresenceFixture() (*PresenceService, *fakeInstances, *fakeChats, *fakePresenceGateway) {
	instances := &fakeInstances{instance: newConnectedInstance()}
	chats := &fakeChats{chat: newDirectChat(instances.instance.ID)}
	gateway := &fakePresenceGateway{}
	return NewPresenceService(instances, chats, gateway, discardLogger()), instances, chats, gateway
}

func TestPresenceSendsEachValidState(t *testing.T) {
	service, instances, chats, gateway := newPresenceFixture()

	for _, state := range []string{PresenceComposing, PresenceRecording, PresencePaused} {
		if err := service.Send(context.Background(), testClientID, instances.instance.ID, chats.chat.ID, state); err != nil {
			t.Fatalf("state %s: %v", state, err)
		}
	}

	if len(gateway.states) != 3 || gateway.chats[0] != chats.chat.JID {
		t.Fatalf("unexpected calls: %+v %+v", gateway.states, gateway.chats)
	}
}

func TestPresenceInvalidStateIsBadRequest(t *testing.T) {
	service, instances, chats, gateway := newPresenceFixture()

	err := service.Send(context.Background(), testClientID, instances.instance.ID, chats.chat.ID, "typing")

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	if len(gateway.states) != 0 {
		t.Fatal("gateway must not be called")
	}
}

func TestPresenceDisconnectedInstanceIsConflict(t *testing.T) {
	service, instances, chats, _ := newPresenceFixture()
	instances.instance.Status = "disconnected"

	err := service.Send(context.Background(), testClientID, instances.instance.ID, chats.chat.ID, PresencePaused)

	requireCode(t, err, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestPresenceUnknownChatIsNotFound(t *testing.T) {
	service, instances, _, _ := newPresenceFixture()

	err := service.Send(context.Background(), testClientID, instances.instance.ID, newConnectedInstance().ID, PresencePaused)

	requireCode(t, err, http.StatusNotFound, "CHAT_NOT_FOUND")
}

func TestPresenceGatewayFailureMapsToBadGateway(t *testing.T) {
	service, instances, chats, gateway := newPresenceFixture()
	gateway.err = errWhatsApp

	err := service.Send(context.Background(), testClientID, instances.instance.ID, chats.chat.ID, PresencePaused)

	requireCode(t, err, http.StatusBadGateway, "WHATSAPP_ERROR")
}
