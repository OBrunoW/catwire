package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/OBrunoW/catwire/internal/domain"
)

func TestCreateInstancePassesBodyToService(t *testing.T) {
	backend := newBackend()
	body := `{"external_id":"crm-user-42","name":"Main","settings":{"ignore_groups":true}}`
	recorder := call{method: http.MethodPost, path: apiPath + "/instances", body: body, key: ownerKey}.send(t, backend.router())
	decoded := expectSuccess(t, recorder, http.StatusCreated)
	if unmarshalData(t, decoded.Data)["id"] != testInstanceID.String() {
		t.Errorf("unexpected data: %s", decoded.Data)
	}
	created := backend.instances.created
	if created.Name != "Main" || created.ExternalID == nil || *created.ExternalID != "crm-user-42" {
		t.Errorf("unexpected input: %+v", created)
	}
	if created.Settings.IgnoreGroups == nil || !*created.Settings.IgnoreGroups {
		t.Error("settings patch was not forwarded")
	}
}

func TestCreateInstanceConflictMapsToEnvelope(t *testing.T) {
	backend := newBackend()
	backend.instances.failure = domain.InstanceAlreadyExists()
	recorder := call{method: http.MethodPost, path: apiPath + "/instances", body: `{"name":"Main"}`, key: ownerKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusConflict, "INSTANCE_ALREADY_EXISTS")
}

func TestListInstancesForwardsFilters(t *testing.T) {
	backend := newBackend()
	query := url.Values{"search": {"sp"}, "status": {"connected"}, "external_id": {"x1"}}
	recorder := call{method: http.MethodGet, path: apiPath + "/instances?" + query.Encode(), key: ownerKey}.send(t, backend.router())
	decoded := expectSuccess(t, recorder, http.StatusOK)
	page := unmarshalData(t, decoded.Data)
	if page["limit"] != float64(50) || page["has_more"] != false {
		t.Errorf("unexpected page: %s", decoded.Data)
	}
	filter := backend.instances.filter
	if filter.Search != "sp" || filter.Status != "connected" || filter.ExternalID != "x1" {
		t.Errorf("unexpected filter: %+v", filter)
	}
}

func TestUpdateInstanceForwardsOnlyProvidedFields(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodPatch, path: instancePath(""), body: `{"name":"New"}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	updated := backend.instances.updated
	if updated.Name == nil || *updated.Name != "New" || updated.ExternalID != nil {
		t.Errorf("unexpected input: %+v", updated)
	}
}

func TestConnectReturnsInstanceAndQR(t *testing.T) {
	recorder := call{method: http.MethodPost, path: instancePath("/connect"), key: ownerKey}.send(t, newBackend().router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	instanceData, ok := data["instance"].(map[string]any)
	if !ok || instanceData["status"] != "qr_required" {
		t.Fatalf("unexpected instance: %v", data["instance"])
	}
	if _, hasSettings := instanceData["settings"].(map[string]any); !hasSettings {
		t.Error("instance must carry settings")
	}
	qr, ok := data["qr"].(map[string]any)
	if !ok || qr["code"] != "2@abc" || qr["expires_at"] == nil {
		t.Fatalf("unexpected qr: %v", data["qr"])
	}
}

func TestConnectErrorsMapToConflict(t *testing.T) {
	backend := newBackend()
	backend.sessions.failure = domain.InstanceAlreadyConnected()
	recorder := call{method: http.MethodPost, path: instancePath("/connect"), key: ownerKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusConflict, "INSTANCE_ALREADY_CONNECTED")
}

func TestQRCodeReturnsStatusAndExpired(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodGet, path: instancePath("/qrcode"), key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	if data["status"] != "qr_required" || data["qr"] == nil {
		t.Errorf("unexpected data: %v", data)
	}
	backend.sessions.failure = domain.QRExpired()
	expired := call{method: http.MethodGet, path: instancePath("/qrcode"), key: ownerKey}.send(t, backend.router())
	expectError(t, expired, http.StatusGone, "QR_EXPIRED")
}

func TestPairCodeForwardsPhone(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodPost, path: instancePath("/pair-code"), body: `{"phone":"5511999999999"}`, key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	if data["code"] != "ABCD-EFGH" || backend.sessions.pairPhone != "5511999999999" {
		t.Errorf("unexpected data %v phone %q", data, backend.sessions.pairPhone)
	}
}

func TestDisconnectAndLogoutReturnUpdatedInstance(t *testing.T) {
	cases := map[string]string{"/disconnect": "disconnected", "/logout": "logged_out"}
	for suffix, status := range cases {
		t.Run(status, func(t *testing.T) {
			recorder := call{method: http.MethodPost, path: instancePath(suffix), key: ownerKey}.send(t, newBackend().router())
			if unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)["status"] != status {
				t.Errorf("unexpected body: %s", recorder.Body.String())
			}
		})
	}
}

func TestLogoutWhenNotPairedIsConflict(t *testing.T) {
	backend := newBackend()
	backend.sessions.failure = domain.InstanceNotPaired()
	recorder := call{method: http.MethodPost, path: instancePath("/logout"), key: ownerKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusConflict, "INSTANCE_NOT_PAIRED")
}

func TestDeleteInstanceCallsSessionManager(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodDelete, path: instancePath(""), key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusNoContent)
	if !backend.sessions.deleted || recorder.Body.Len() != 0 {
		t.Errorf("deleted=%v body=%q", backend.sessions.deleted, recorder.Body.String())
	}
}

func TestDeleteOfOtherClientDoesNotReachSessionManager(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodDelete, path: instancePath(""), key: otherKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusNotFound, "INSTANCE_NOT_FOUND")
	if backend.sessions.deleted {
		t.Error("session manager must not run for another client")
	}
}

func TestUpdateSettingsForwardsPatch(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodPatch, path: instancePath("/settings"), body: `{"always_online":true}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	patch := backend.settings.patch
	if patch.AlwaysOnline == nil || !*patch.AlwaysOnline || patch.SyncHistory != nil {
		t.Errorf("unexpected patch: %+v", patch)
	}
}

func TestGetSettingsReturnsObject(t *testing.T) {
	recorder := call{method: http.MethodGet, path: instancePath("/settings"), key: ownerKey}.send(t, newBackend().router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	if data["sync_history"] != true || data["download_media"] != true {
		t.Errorf("unexpected settings: %v", data)
	}
}

func TestProfileEndpoints(t *testing.T) {
	backend := newBackend()
	get := call{method: http.MethodGet, path: instancePath("/profile"), key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, get, http.StatusOK).Data)
	if data["push_name"] != "Main" || data["status_message"] != "hello" {
		t.Errorf("unexpected profile: %v", data)
	}
	if _, present := data["picture_url"]; !present {
		t.Error("picture_url must be present")
	}
	patch := call{method: http.MethodPatch, path: instancePath("/profile"), body: `{"status_message":"away"}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, patch, http.StatusOK)
	update := backend.profiles.update
	if update.StatusMessage == nil || *update.StatusMessage != "away" || update.PushName != nil {
		t.Errorf("unexpected update: %+v", update)
	}
}

func TestPrivacyEndpoints(t *testing.T) {
	backend := newBackend()
	get := call{method: http.MethodGet, path: instancePath("/privacy"), key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, get, http.StatusOK).Data)
	if data["last_seen"] != "contacts" || data["call_add"] == nil {
		t.Errorf("unexpected privacy: %v", data)
	}
	patch := call{method: http.MethodPatch, path: instancePath("/privacy"), body: `{"last_seen":"none"}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, patch, http.StatusOK)
	if update := backend.privacy.update; update.LastSeen == nil || *update.LastSeen != "none" || update.Online != nil {
		t.Errorf("unexpected update: %+v", update)
	}
}

func TestCheckContactsForwardsPhones(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodPost, path: instancePath("/contacts/check"), body: `{"phones":["5511988887777","11988887777"]}`, key: ownerKey}.send(t, backend.router())
	decoded := expectSuccess(t, recorder, http.StatusOK)
	if !strings.HasPrefix(string(decoded.Data), "[") || len(backend.contacts.phones) != 2 {
		t.Errorf("unexpected data %s phones %v", decoded.Data, backend.contacts.phones)
	}
}

func TestSendMessageReturnsAcceptedWithIdempotencyKey(t *testing.T) {
	backend := newBackend()
	body := `{"to":"5511988887777","type":"text","text":"hi","metadata":{"conversation_id":"c-1"}}`
	recorder := call{method: http.MethodPost, path: instancePath("/messages"), body: body, key: ownerKey, headers: map[string]string{"Idempotency-Key": "abc"}}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusAccepted).Data)
	if data["wa_message_id"] != "3EB0" || data["status"] != "pending" {
		t.Errorf("unexpected message: %v", data)
	}
	if backend.sender.key != "abc" || backend.sender.request.To != "5511988887777" || backend.sender.request.Type != "text" {
		t.Errorf("unexpected request %+v key %q", backend.sender.request, backend.sender.key)
	}
}

func TestSendMessageReplayReturnsOK(t *testing.T) {
	backend := newBackend()
	backend.sender.replayed = true
	recorder := call{method: http.MethodPost, path: instancePath("/messages"), body: `{"to":"5511988887777","type":"text","text":"hi"}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
}

func TestSendMessageDomainErrorsKeepStatusAndCode(t *testing.T) {
	cases := []struct {
		failure error
		status  int
		code    string
	}{
		{domain.InstanceNotConnected(), http.StatusConflict, "INSTANCE_NOT_CONNECTED"},
		{domain.IdempotencyConflict(), http.StatusConflict, "IDEMPOTENCY_CONFLICT"},
		{domain.RecipientNotOnWhatsApp(), http.StatusUnprocessableEntity, "RECIPIENT_NOT_ON_WHATSAPP"},
		{domain.MediaTooLarge(), http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE"},
		{domain.UnsupportedMediaType("no"), http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE"},
	}
	for _, item := range cases {
		t.Run(item.code, func(t *testing.T) {
			backend := newBackend()
			backend.sender.failure = item.failure
			recorder := call{method: http.MethodPost, path: instancePath("/messages"), body: `{"to":"5511988887777","type":"text","text":"hi"}`, key: ownerKey}.send(t, backend.router())
			expectError(t, recorder, item.status, item.code)
		})
	}
}

func TestSendMessageBodyAboveLimitIsMediaTooLarge(t *testing.T) {
	backend := newBackend()
	backend.maxMedia = 16
	body := `{"to":"5511988887777","type":"image","media":{"base64":"` + strings.Repeat("A", 200) + `"}}`
	recorder := call{method: http.MethodPost, path: instancePath("/messages"), body: body, key: ownerKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE")
}

func TestListMessagesParsesFilters(t *testing.T) {
	backend := newBackend()
	query := url.Values{
		"chat_id": {testChatID.String()}, "wa_message_id": {"3EB0"}, "direction": {"inbound"},
		"type": {"text"}, "source": {"live"}, "since": {"2026-11-22T10:00:00Z"},
		"until": {"2026-11-22T12:00:00Z"}, "search": {"nota"},
	}
	recorder := call{method: http.MethodGet, path: instancePath("/messages?" + query.Encode()), key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	filter := backend.messages.filter
	if filter.ChatID == nil || *filter.ChatID != testChatID || filter.WAMessageID != "3EB0" || filter.Direction != "inbound" {
		t.Errorf("unexpected filter: %+v", filter)
	}
	if filter.Since == nil || filter.Until == nil || filter.Type != "text" || filter.Source != "live" || filter.Search != "nota" {
		t.Errorf("unexpected filter: %+v", filter)
	}
}

func TestListChatsParsesFilters(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodGet, path: instancePath("/chats?search=ma&type=group"), key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	if backend.chats.filter.Search != "ma" || backend.chats.filter.Type != "group" {
		t.Errorf("unexpected filter: %+v", backend.chats.filter)
	}
}

func TestMarkReadWithAndWithoutBody(t *testing.T) {
	backend := newBackend()
	path := instancePath("/chats/" + testChatID.String() + "/read")
	empty := call{method: http.MethodPost, path: path, key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, empty, http.StatusOK).Data)
	if data["marked"] != float64(3) || data["chat_id"] != testChatID.String() || backend.reads.messageIDs != nil {
		t.Errorf("unexpected data %v ids %v", data, backend.reads.messageIDs)
	}
	body := `{"message_ids":["` + testMessageID.String() + `"]}`
	listed := call{method: http.MethodPost, path: path, body: body, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, listed, http.StatusOK)
	if len(backend.reads.messageIDs) != 1 || backend.reads.messageIDs[0] != testMessageID {
		t.Errorf("unexpected ids: %v", backend.reads.messageIDs)
	}
}

func TestPresenceAndHistoryForwardBody(t *testing.T) {
	backend := newBackend()
	chat := "/chats/" + testChatID.String()
	presence := call{method: http.MethodPost, path: instancePath(chat + "/presence"), body: `{"state":"recording"}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, presence, http.StatusNoContent)
	if backend.presence.state != "recording" {
		t.Errorf("state: got %q", backend.presence.state)
	}
	history := call{method: http.MethodPost, path: instancePath(chat + "/history"), body: `{"count":20}`, key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, history, http.StatusAccepted).Data)
	if data["request_id"] != "3EB0REQ" || data["chat_id"] != testChatID.String() || backend.history.count != 20 {
		t.Errorf("unexpected data %v count %d", data, backend.history.count)
	}
}

func TestCreateWebhookReturnsSecret(t *testing.T) {
	backend := newBackend()
	body := `{"url":"https://x.test/h","description":"inbox","events":["whatsapp.message.received"],"instance_ids":["` + testInstanceID.String() + `"],"active":false}`
	recorder := call{method: http.MethodPost, path: apiPath + "/webhooks", body: body, key: ownerKey}.send(t, backend.router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusCreated).Data)
	if data["secret"] != "s3cret" || data["id"] != testWebhookID.String() {
		t.Errorf("unexpected data: %v", data)
	}
	created := backend.webhooks.created
	if created.URL != "https://x.test/h" || len(created.Events) != 1 || len(created.InstanceIDs) != 1 || created.Active == nil || *created.Active {
		t.Errorf("unexpected input: %+v", created)
	}
}

func TestUpdateWebhookDistinguishesAbsentFromEmpty(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodPatch, path: webhookPath(""), body: `{"events":[]}`, key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	updated := backend.webhooks.updated
	if updated.Events == nil || len(*updated.Events) != 0 || updated.InstanceIDs != nil || updated.URL != nil {
		t.Errorf("unexpected update: %+v", updated)
	}
}

func TestListWebhooksParsesActiveFilter(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodGet, path: apiPath + "/webhooks?active=true", key: ownerKey}.send(t, backend.router())
	expectSuccess(t, recorder, http.StatusOK)
	if backend.webhooks.active == nil || !*backend.webhooks.active {
		t.Error("active filter was not forwarded")
	}
}

func TestWebhookSecretTestAndRetryResponses(t *testing.T) {
	router := newBackend().router()
	rotate := call{method: http.MethodPost, path: webhookPath("/rotate-secret"), key: ownerKey}.send(t, router)
	rotated := unmarshalData(t, expectSuccess(t, rotate, http.StatusOK).Data)
	if rotated["secret"] != "new" || rotated["id"] != testWebhookID.String() {
		t.Errorf("unexpected rotate: %v", rotated)
	}
	test := call{method: http.MethodPost, path: webhookPath("/test"), key: ownerKey}.send(t, router)
	if unmarshalData(t, expectSuccess(t, test, http.StatusAccepted).Data)["delivery_id"] != testDeliveryID.String() {
		t.Errorf("unexpected test body: %s", test.Body.String())
	}
	retry := call{method: http.MethodPost, path: webhookPath("/deliveries/" + testDeliveryID.String() + "/retry"), key: ownerKey}.send(t, router)
	delivery := unmarshalData(t, expectSuccess(t, retry, http.StatusAccepted).Data)
	if delivery["status"] != "pending" || delivery["webhook_id"] != testWebhookID.String() {
		t.Errorf("unexpected delivery: %v", delivery)
	}
}

func TestListDeliveriesMapsRowsAndFilters(t *testing.T) {
	backend := newBackend()
	recorder := call{method: http.MethodGet, path: webhookPath("/deliveries?status=failed&event_type=webhook.test"), key: ownerKey}.send(t, backend.router())
	page := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	rows, ok := page["data"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("unexpected page: %v", page)
	}
	row := rows[0].(map[string]any)
	if row["id"] != testDeliveryID.String() || row["event_type"] != "webhook.test" {
		t.Errorf("unexpected row: %v", row)
	}
	if _, leaked := row["payload"]; leaked {
		t.Error("delivery payload must not be exposed")
	}
	if backend.webhooks.filter.Status != "failed" || backend.webhooks.filter.EventType != "webhook.test" {
		t.Errorf("unexpected filter: %+v", backend.webhooks.filter)
	}
}
