package operations

import (
	"context"
	"net/http"
	"testing"
)

func newPrivacyFixture() (*PrivacyService, *fakeInstances, *fakePrivacyGateway) {
	instances := &fakeInstances{instance: newConnectedInstance()}
	gateway := &fakePrivacyGateway{settings: PrivacySettings{GroupAdd: "contacts", ReadReceipts: "all"}}
	return NewPrivacyService(instances, gateway, discardLogger()), instances, gateway
}

func TestPrivacyGetReturnsGatewaySettings(t *testing.T) {
	service, instances, _ := newPrivacyFixture()

	settings, err := service.Get(context.Background(), testClientID, instances.instance.ID)

	if err != nil || settings.GroupAdd != "contacts" {
		t.Fatalf("unexpected settings: %+v err=%v", settings, err)
	}
}

func TestPrivacyUpdateAppliesEachChange(t *testing.T) {
	service, instances, gateway := newPrivacyFixture()

	settings, err := service.Update(context.Background(), testClientID, instances.instance.ID, PrivacyUpdate{
		ReadReceipts: text("none"), GroupAdd: text("contacts"),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gateway.changes) != 2 || gateway.changes[0].Key != PrivacyKeyGroupAdd || gateway.changes[1].Key != PrivacyKeyReadReceipts {
		t.Fatalf("unexpected changes: %+v", gateway.changes)
	}
	if settings.ReadReceipts != "none" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestPrivacyUpdateRejectsValueNotAcceptedForKey(t *testing.T) {
	service, instances, gateway := newPrivacyFixture()
	cases := []PrivacyUpdate{
		{ReadReceipts: text("contacts")},
		{Online: text("none")},
		{CallAdd: text("contacts")},
		{LastSeen: text("everyone")},
		{GroupAdd: text("")},
	}
	for _, update := range cases {
		_, err := service.Update(context.Background(), testClientID, instances.instance.ID, update)
		requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	}
	if len(gateway.changes) != 0 {
		t.Fatal("gateway must not be called for invalid input")
	}
}

func TestPrivacyUpdateValidatesEveryChangeBeforeApplying(t *testing.T) {
	service, instances, gateway := newPrivacyFixture()

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, PrivacyUpdate{
		GroupAdd: text("none"), ReadReceipts: text("known"),
	})

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	if len(gateway.changes) != 0 {
		t.Fatalf("partial change applied: %+v", gateway.changes)
	}
}

func TestPrivacyUpdateWithoutKeysIsBadRequest(t *testing.T) {
	service, instances, _ := newPrivacyFixture()

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, PrivacyUpdate{})

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
}

func TestPrivacyDisconnectedInstanceIsConflict(t *testing.T) {
	service, instances, _ := newPrivacyFixture()
	instances.instance.Status = "disconnected"

	_, getErr := service.Get(context.Background(), testClientID, instances.instance.ID)
	_, updateErr := service.Update(context.Background(), testClientID, instances.instance.ID, PrivacyUpdate{Online: text("all")})

	requireCode(t, getErr, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
	requireCode(t, updateErr, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestPrivacyGatewayFailureMapsToBadGateway(t *testing.T) {
	service, instances, gateway := newPrivacyFixture()
	gateway.err = errWhatsApp

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, PrivacyUpdate{Online: text("all")})

	requireCode(t, err, http.StatusBadGateway, "WHATSAPP_ERROR")
}
