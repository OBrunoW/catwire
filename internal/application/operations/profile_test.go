package operations

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func newProfileFixture() (*ProfileService, *fakeInstances, *fakeProfileGateway) {
	instances := &fakeInstances{instance: newConnectedInstance()}
	gateway := &fakeProfileGateway{data: ProfileData{PushName: "Old", StatusMessage: "Old status"}}
	return NewProfileService(instances, gateway, discardLogger()), instances, gateway
}

func text(value string) *string {
	return &value
}

func TestProfileUpdateAppliesStatusAndPushName(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	gateway.data.StatusMessage = "Open 8 to 18"

	profile, err := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{
		PushName: text("  New Name  "), StatusMessage: text("Open 8 to 18"),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gateway.pushCalls) != 1 || gateway.pushCalls[0] != "New Name" || len(gateway.statusCalls) != 1 {
		t.Fatalf("unexpected gateway calls: %+v %+v", gateway.pushCalls, gateway.statusCalls)
	}
	if profile.PushName != "New Name" || profile.StatusMessage != "Open 8 to 18" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestProfileUpdateWithoutFieldsIsBadRequest(t *testing.T) {
	service, instances, _ := newProfileFixture()

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{})

	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
}

func TestProfileUpdateRejectsInvalidValues(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	cases := []ProfileUpdate{
		{PushName: text("   ")},
		{PushName: text(strings.Repeat("a", 26))},
		{StatusMessage: text(strings.Repeat("a", 140))},
	}
	for _, update := range cases {
		_, err := service.Update(context.Background(), testClientID, instances.instance.ID, update)
		requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
	}
	if len(gateway.pushCalls)+len(gateway.statusCalls) != 0 {
		t.Fatal("gateway must not be called for invalid input")
	}
}

func TestProfileDisconnectedInstanceIsConflict(t *testing.T) {
	service, instances, _ := newProfileFixture()
	instances.instance.Status = "disconnected"

	_, getErr := service.Get(context.Background(), testClientID, instances.instance.ID)
	_, updateErr := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{PushName: text("A")})

	requireCode(t, getErr, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
	requireCode(t, updateErr, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestProfileUnpairedInstanceIsConflict(t *testing.T) {
	service, instances, _ := newProfileFixture()
	instances.instance.Status = "created"
	instances.instance.JID = nil

	_, err := service.Get(context.Background(), testClientID, instances.instance.ID)

	requireCode(t, err, http.StatusConflict, "INSTANCE_NOT_PAIRED")
}

func TestProfileOtherClientSeesNotFound(t *testing.T) {
	service, instances, _ := newProfileFixture()

	_, err := service.Get(context.Background(), "other", instances.instance.ID)

	requireCode(t, err, http.StatusNotFound, "INSTANCE_NOT_FOUND")
}

func TestProfileGatewayDisconnectionMapsToConflict(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	gateway.err = ErrInstanceDisconnected

	_, err := service.Get(context.Background(), testClientID, instances.instance.ID)

	requireCode(t, err, http.StatusConflict, "INSTANCE_NOT_CONNECTED")
}

func TestProfileGatewayFailureMapsToBadGatewayWithoutLeakingCause(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	gateway.err = errWhatsApp

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{StatusMessage: text("x")})

	requireCode(t, err, http.StatusBadGateway, "WHATSAPP_ERROR")
	if strings.Contains(err.Error(), errWhatsApp.Error()) {
		t.Fatalf("cause leaked: %v", err)
	}
}

func TestProfileGetReturnsBusinessNameFromInstance(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	instances.instance.BusinessName = text("Godi")
	gateway.data.PictureURL = text("https://pps.whatsapp.net/a")

	profile, err := service.Get(context.Background(), testClientID, instances.instance.ID)

	if err != nil || profile.BusinessName == nil || *profile.BusinessName != "Godi" || profile.PictureURL == nil {
		t.Fatalf("unexpected profile: %+v err=%v", profile, err)
	}
}

func TestProfileUpdateBlankStatusMessageClears(t *testing.T) {
	service, instances, gateway := newProfileFixture()

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{StatusMessage: text("   ")})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gateway.statusCalls) != 1 || gateway.statusCalls[0] != "" {
		t.Fatalf("unexpected status calls: %+v", gateway.statusCalls)
	}
}

func TestProfileUpdateStatusMessageLimitAppliesAfterTrim(t *testing.T) {
	service, instances, gateway := newProfileFixture()
	padded := "  " + strings.Repeat("a", 139) + "  "

	_, err := service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{StatusMessage: text(padded)})

	if err != nil || len(gateway.statusCalls) != 1 || len(gateway.statusCalls[0]) != 139 {
		t.Fatalf("unexpected result: err=%v calls=%+v", err, gateway.statusCalls)
	}
	_, err = service.Update(context.Background(), testClientID, instances.instance.ID, ProfileUpdate{StatusMessage: text(strings.Repeat("a", 140))})
	requireCode(t, err, http.StatusBadRequest, "BAD_REQUEST")
}
