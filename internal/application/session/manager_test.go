package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

func decodePayload(t *testing.T, event domain.OutboxEvent) statusChangedPayload {
	t.Helper()
	var payload statusChangedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}

func assertStatusEvent(t *testing.T, h *harness, status, previous string) {
	t.Helper()
	event := h.store.lastOutbox()
	if event.EventType != EventTypeInstanceStatusChanged {
		t.Fatalf("event type = %s", event.EventType)
	}
	payload := decodePayload(t, event)
	if payload.Status != status || payload.PreviousStatus != previous {
		t.Fatalf("payload = %+v, want %s from %s", payload, status, previous)
	}
}

func TestConnectPairedMovesToConnectingThenConnected(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	result, err := h.manager.Connect(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if result.QR != nil || result.Instance.Status != domain.InstanceStatusConnecting {
		t.Fatalf("result = %+v", result)
	}
	assertStatusEvent(t, h, domain.InstanceStatusConnecting, domain.InstanceStatusDisconnected)
	h.feed(t, instance.ID, Event{Kind: EventConnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusConnected)
	assertStatusEvent(t, h, domain.InstanceStatusConnected, domain.InstanceStatusConnecting)
}

func TestConnectPairedWhenConnectedFails(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.feed(t, instance.ID, Event{Kind: EventConnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusConnected)
	_, err := h.manager.Connect(context.Background(), instance.ID)
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Code != "INSTANCE_ALREADY_CONNECTED" {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectPairedWhatsAppErrorMapsTo502(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) { client.connectErr = errBoom }
	_, err := h.manager.Connect(context.Background(), instance.ID)
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Code != "WHATSAPP_ERROR" || appErr.Status != 502 {
		t.Fatalf("err = %v", err)
	}
	if h.store.outboxCount() != 0 {
		t.Fatal("no event expected on failed connect")
	}
}

func TestConnectUnpairedOpensQRAndExposesCode(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	result, err := h.manager.Connect(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if result.QR == nil || result.QR.Code != "2@abc" || !result.QR.ExpiresAt.After(time.Now()) {
		t.Fatalf("qr = %+v", result.QR)
	}
	if result.Instance.Status != domain.InstanceStatusQRRequired {
		t.Fatalf("status = %s", result.Instance.Status)
	}
	client := h.factory.last()
	if !client.called("open_qr") || !client.called("connect") {
		t.Fatalf("calls = %v", client.calls)
	}
	assertStatusEvent(t, h, domain.InstanceStatusQRRequired, domain.InstanceStatusCreated)
}

func TestConnectUnpairedReturnsSameQRWhileActive(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	result, err := h.manager.Connect(context.Background(), instance.ID)
	if err != nil || result.QR == nil || result.QR.Code != "2@abc" {
		t.Fatalf("second connect = %+v, %v", result, err)
	}
	if h.factory.count() != 1 {
		t.Fatalf("clients created = %d", h.factory.count())
	}
}

func TestConnectUnpairedWithoutQRTimesOutAndReverts(t *testing.T) {
	instance := unpairedInstance()
	store := newFakeStore(instance)
	factory := &fakeFactory{}
	manager := NewManager(Options{Instances: store, Unit: store, Clients: factory, QRWait: 20 * time.Millisecond})
	t.Cleanup(manager.Stop)
	_, err := manager.Connect(context.Background(), instance.ID)
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "WHATSAPP_ERROR" {
		t.Fatalf("err = %v", err)
	}
	if store.snapshot(instance.ID).Status != domain.InstanceStatusDisconnected {
		t.Fatalf("status = %s", store.snapshot(instance.ID).Status)
	}
}

func TestQRReturnsActiveCodeAndGoneWhenEnded(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	code, err := h.manager.QR(context.Background(), instance.ID)
	if err != nil || code.Code != "2@abc" {
		t.Fatalf("qr = %+v, %v", code, err)
	}
	h.feed(t, instance.ID, Event{Kind: EventQRTimeout})
	h.waitStatus(t, instance.ID, domain.InstanceStatusDisconnected)
	_, err = h.manager.QR(context.Background(), instance.ID)
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "QR_EXPIRED" {
		t.Fatalf("err = %v", err)
	}
}

func TestQRTimeoutTransitionEmitsEvent(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.factory.last().qrItems <- QRItem{Kind: QRItemTimeout}
	h.waitStatus(t, instance.ID, domain.InstanceStatusDisconnected)
	assertStatusEvent(t, h, domain.InstanceStatusDisconnected, domain.InstanceStatusQRRequired)
	if reason := decodePayload(t, h.store.lastOutbox()).Reason; reason == nil || *reason != reasonQRTimeout {
		t.Fatalf("reason = %v", reason)
	}
}

func TestQRFailureAndClientOutdatedFromChannel(t *testing.T) {
	cases := []struct {
		name   string
		item   QRItem
		status string
	}{
		{"failed", QRItem{Kind: QRItemFailed, Reason: "pair error"}, domain.InstanceStatusDisconnected},
		{"outdated", QRItem{Kind: QRItemClientOutdated}, domain.InstanceStatusDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := unpairedInstance()
			h := newHarness(t, instance)
			h.factory.prepare = func(client *fakeClient) {
				client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
			}
			if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
				t.Fatalf("connect: %v", err)
			}
			h.factory.last().qrItems <- tc.item
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && h.store.snapshot(instance.ID).Status == domain.InstanceStatusQRRequired {
				time.Sleep(5 * time.Millisecond)
			}
			if h.store.snapshot(instance.ID).Status == domain.InstanceStatusQRRequired {
				t.Fatal("status did not change")
			}
		})
	}
}

func TestPairedEventStoresIdentityAndMovesToConnecting(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	jid := "5511888888888:2@s.whatsapp.net"
	phone := "5511888888888"
	h.feed(t, instance.ID, Event{Kind: EventPaired, Identity: domain.InstanceIdentity{JID: &jid, Phone: &phone}})
	updated := h.waitStatus(t, instance.ID, domain.InstanceStatusConnecting)
	if updated.JID == nil || *updated.JID != jid {
		t.Fatalf("jid = %v", updated.JID)
	}
	payload := decodePayload(t, h.store.lastOutbox())
	if payload.JID == nil || *payload.JID != jid || payload.Phone == nil || *payload.Phone != phone {
		t.Fatalf("payload = %+v", payload)
	}
	if _, err := h.manager.QR(context.Background(), instance.ID); err == nil {
		t.Fatal("qr must not be available after pairing")
	}
}

func TestPairPhoneRequiresQRRequired(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	_, err := h.manager.PairPhone(context.Background(), instance.ID, "5511999999999")
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "BAD_REQUEST" {
		t.Fatalf("err = %v", err)
	}
}

func TestPairPhoneValidatesPhone(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	for _, phone := range []string{"", "123", "+5511999999999", "55119999abcde"} {
		_, err := h.manager.PairPhone(context.Background(), instance.ID, phone)
		if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "BAD_REQUEST" {
			t.Fatalf("phone %q err = %v", phone, err)
		}
	}
}

func TestPairPhoneReturnsCodeWhenQRRequired(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "2@abc", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	pair, err := h.manager.PairPhone(context.Background(), instance.ID, "5511999999999")
	if err != nil || pair.Code != "ABCD-EFGH" || !pair.ExpiresAt.After(time.Now()) {
		t.Fatalf("pair = %+v, %v", pair, err)
	}
	if !h.factory.last().called("pair_phone:5511999999999") {
		t.Fatal("client was not asked to pair")
	}
}

func TestPairPhoneWhenConnectedFails(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	_, err := h.manager.PairPhone(context.Background(), instance.ID, "5511999999999")
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "INSTANCE_ALREADY_CONNECTED" {
		t.Fatalf("err = %v", err)
	}
}

func TestDisconnectedEventOnConnectedInstance(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.feed(t, instance.ID, Event{Kind: EventConnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusConnected)
	h.feed(t, instance.ID, Event{Kind: EventDisconnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusDisconnected)
	assertStatusEvent(t, h, domain.InstanceStatusDisconnected, domain.InstanceStatusConnected)
}

func TestDisconnectedEventIgnoredWhenAlreadyDisconnected(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	h.feed(t, instance.ID, Event{Kind: EventDisconnected})
	h.feed(t, instance.ID, Event{Kind: EventConnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusConnected)
	if h.store.outboxCount() != 1 {
		t.Fatalf("outbox = %d", h.store.outboxCount())
	}
}

func TestDisconnectClosesWithoutUnpairingAndDisablesAutoConnect(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.feed(t, instance.ID, Event{Kind: EventConnected})
	h.waitStatus(t, instance.ID, domain.InstanceStatusConnected)
	updated, err := h.manager.Disconnect(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if updated.Status != domain.InstanceStatusDisconnected || updated.AutoConnect || updated.JID == nil {
		t.Fatalf("instance = %+v", updated)
	}
	client := h.factory.last()
	if !client.called("disconnect") || client.called("logout") || !client.released {
		t.Fatalf("calls = %v", client.calls)
	}
	assertStatusEvent(t, h, domain.InstanceStatusDisconnected, domain.InstanceStatusConnected)
}

func TestLogoutUnlinksDeletesDeviceAndClearsIdentity(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	updated, err := h.manager.Logout(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	client := h.factory.last()
	if !client.called("logout") || !client.called("delete_device") {
		t.Fatalf("calls = %v", client.calls)
	}
	if updated.Status != domain.InstanceStatusLoggedOut || updated.JID != nil || updated.AutoConnect {
		t.Fatalf("instance = %+v", updated)
	}
	assertStatusEvent(t, h, domain.InstanceStatusLoggedOut, domain.InstanceStatusConnected)
}

func TestLogoutUnpairedReturnsNotPaired(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	_, err := h.manager.Logout(context.Background(), instance.ID)
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "INSTANCE_NOT_PAIRED" {
		t.Fatalf("err = %v", err)
	}
}

func TestLogoutRemoteFailureKeepsState(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) { client.logoutErr = errBoom }
	_, err := h.manager.Logout(context.Background(), instance.ID)
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != "WHATSAPP_ERROR" {
		t.Fatalf("err = %v", err)
	}
	if h.store.snapshot(instance.ID).Status != domain.InstanceStatusConnected || h.store.outboxCount() != 0 {
		t.Fatal("state must not change when remote logout fails")
	}
}

func TestLoggedOutEventFromPhoneClearsIdentityAndStopsAutoConnect(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.feed(t, instance.ID, Event{Kind: EventLoggedOut})
	updated := h.waitStatus(t, instance.ID, domain.InstanceStatusLoggedOut)
	if updated.JID != nil || updated.AutoConnect {
		t.Fatalf("instance = %+v", updated)
	}
	if reason := decodePayload(t, h.store.lastOutbox()).Reason; reason == nil || *reason != reasonLoggedOutFromPhone {
		t.Fatalf("reason = %v", reason)
	}
}

func TestDeletePairedLogsOutEmitsEventAndSoftDeletes(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	if err := h.manager.Delete(context.Background(), instance.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	client := h.factory.last()
	if !client.called("logout") || !client.called("delete_device") {
		t.Fatalf("calls = %v", client.calls)
	}
	assertStatusEvent(t, h, domain.InstanceStatusLoggedOut, domain.InstanceStatusConnected)
	if len(h.store.deleted) != 1 {
		t.Fatal("instance was not soft deleted")
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err == nil {
		t.Fatal("deleted instance must not be found")
	}
}

func TestDeletePairedIgnoresRemoteLogoutFailure(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) { client.logoutErr = errBoom }
	if err := h.manager.Delete(context.Background(), instance.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !h.factory.last().called("delete_device") || len(h.store.deleted) != 1 {
		t.Fatal("device and instance must be removed despite the remote failure")
	}
}

func TestDeleteUnpairedEmitsNoEvent(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	if err := h.manager.Delete(context.Background(), instance.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if h.store.outboxCount() != 0 || len(h.store.deleted) != 1 {
		t.Fatalf("outbox = %d deleted = %d", h.store.outboxCount(), len(h.store.deleted))
	}
}

func TestTerminalEventsMapToStatuses(t *testing.T) {
	cases := []struct {
		name        string
		event       Event
		status      string
		autoConnect bool
	}{
		{"stream replaced", Event{Kind: EventStreamReplaced}, domain.InstanceStatusDisconnected, false},
		{"temporary ban", Event{Kind: EventTemporaryBan}, domain.InstanceStatusBanned, false},
		{"banned", Event{Kind: EventBanned}, domain.InstanceStatusBanned, false},
		{"client outdated", Event{Kind: EventClientOutdated}, domain.InstanceStatusClientOutdated, true},
		{"connect failure", Event{Kind: EventConnectFailure}, domain.InstanceStatusDisconnected, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := pairedInstance(domain.InstanceStatusDisconnected)
			h := newHarness(t, instance)
			if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
				t.Fatalf("connect: %v", err)
			}
			h.feed(t, instance.ID, tc.event)
			updated := h.waitStatus(t, instance.ID, tc.status)
			if updated.AutoConnect != tc.autoConnect {
				t.Fatalf("auto connect = %v", updated.AutoConnect)
			}
			assertStatusEvent(t, h, tc.status, domain.InstanceStatusConnecting)
		})
	}
}

func TestStaleGenerationEventIsIgnored(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	stale := h.factory.last().emit
	if _, err := h.manager.Disconnect(context.Background(), instance.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	before := h.store.outboxCount()
	stale(Event{Kind: EventConnected})
	h.feed(t, instance.ID, Event{Kind: EventDisconnected})
	time.Sleep(50 * time.Millisecond)
	if h.store.outboxCount() != before || h.store.snapshot(instance.ID).Status != domain.InstanceStatusDisconnected {
		t.Fatal("stale event must be ignored")
	}
}

func TestStartReconnectsEligibleInstancesOnly(t *testing.T) {
	connectable := pairedInstance(domain.InstanceStatusConnected)
	banned := pairedInstance(domain.InstanceStatusBanned)
	manual := pairedInstance(domain.InstanceStatusDisconnected)
	manual.AutoConnect = false
	store := newFakeStore(connectable, banned, manual)
	factory := &fakeFactory{}
	manager := NewManager(Options{Instances: store, Unit: store, Clients: factory, WatchdogInterval: time.Hour})
	t.Cleanup(manager.Stop)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if factory.count() != 1 {
		t.Fatalf("clients created = %d", factory.count())
	}
	if store.snapshot(connectable.ID).Status != domain.InstanceStatusConnecting {
		t.Fatalf("status = %s", store.snapshot(connectable.ID).Status)
	}
	assertStatusEventFor(t, store, domain.InstanceStatusConnecting, domain.InstanceStatusConnected)
}

func assertStatusEventFor(t *testing.T, store *fakeStore, status, previous string) {
	t.Helper()
	payload := decodePayload(t, store.lastOutbox())
	if payload.Status != status || payload.PreviousStatus != previous {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestStartMarksMissingDeviceAsLoggedOut(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	store := newFakeStore(instance)
	factory := &fakeFactory{createErr: ErrDeviceNotFound}
	manager := NewManager(Options{Instances: store, Unit: store, Clients: factory, WatchdogInterval: time.Hour})
	t.Cleanup(manager.Stop)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	updated := store.snapshot(instance.ID)
	if updated.Status != domain.InstanceStatusLoggedOut || updated.JID != nil {
		t.Fatalf("instance = %+v", updated)
	}
}

func TestWatchdogReconnectsDroppedClientAndSkipsClientOutdated(t *testing.T) {
	dropped := pairedInstance(domain.InstanceStatusDisconnected)
	outdated := pairedInstance(domain.InstanceStatusClientOutdated)
	store := newFakeStore(dropped, outdated)
	factory := &fakeFactory{}
	manager := NewManager(Options{Instances: store, Unit: store, Clients: factory, WatchdogInterval: 10 * time.Millisecond})
	t.Cleanup(manager.Stop)
	if err := manager.reconnectAll(context.Background(), shouldReconnectOnWatchdog); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if factory.count() != 1 || store.snapshot(outdated.ID).Status != domain.InstanceStatusClientOutdated {
		t.Fatalf("clients = %d", factory.count())
	}
	factory.last().Disconnect()
	if err := manager.reconnectAll(context.Background(), shouldReconnectOnWatchdog); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if !factory.last().IsConnected() || factory.count() != 1 {
		t.Fatal("watchdog must reuse the client and reconnect it")
	}
}

func TestWatchdogTickReconnects(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	store := newFakeStore(instance)
	factory := &fakeFactory{}
	manager := NewManager(Options{Instances: store, Unit: store, Clients: factory, WatchdogInterval: 10 * time.Millisecond})
	t.Cleanup(manager.Stop)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	factory.last().Disconnect()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !factory.last().IsConnected() {
		time.Sleep(5 * time.Millisecond)
	}
	if !factory.last().IsConnected() {
		t.Fatal("watchdog did not reconnect")
	}
}
