package handlers

import (
	"net/http"
	"testing"
)

type route struct {
	name   string
	method string
	path   string
	body   string
	status int
	scoped bool
}

func instancePath(suffix string) string {
	return apiPath + "/instances/" + testInstanceID.String() + suffix
}

func webhookPath(suffix string) string {
	return apiPath + "/webhooks/" + testWebhookID.String() + suffix
}

func allRoutes() []route {
	chat := "/chats/" + testChatID.String()
	return []route{
		{"create instance", http.MethodPost, apiPath + "/instances", `{"name":"Main"}`, http.StatusCreated, false},
		{"list instances", http.MethodGet, apiPath + "/instances", "", http.StatusOK, false},
		{"get instance", http.MethodGet, instancePath(""), "", http.StatusOK, true},
		{"update instance", http.MethodPatch, instancePath(""), `{"name":"New"}`, http.StatusOK, true},
		{"delete instance", http.MethodDelete, instancePath(""), "", http.StatusNoContent, true},
		{"connect", http.MethodPost, instancePath("/connect"), "", http.StatusOK, true},
		{"qrcode", http.MethodGet, instancePath("/qrcode"), "", http.StatusOK, true},
		{"pair code", http.MethodPost, instancePath("/pair-code"), `{"phone":"5511999999999"}`, http.StatusOK, true},
		{"disconnect", http.MethodPost, instancePath("/disconnect"), "", http.StatusOK, true},
		{"logout", http.MethodPost, instancePath("/logout"), "", http.StatusOK, true},
		{"get settings", http.MethodGet, instancePath("/settings"), "", http.StatusOK, true},
		{"update settings", http.MethodPatch, instancePath("/settings"), `{"sync_history":false}`, http.StatusOK, true},
		{"get profile", http.MethodGet, instancePath("/profile"), "", http.StatusOK, true},
		{"update profile", http.MethodPatch, instancePath("/profile"), `{"push_name":"Main"}`, http.StatusOK, true},
		{"get privacy", http.MethodGet, instancePath("/privacy"), "", http.StatusOK, true},
		{"update privacy", http.MethodPatch, instancePath("/privacy"), `{"last_seen":"none"}`, http.StatusOK, true},
		{"check contacts", http.MethodPost, instancePath("/contacts/check"), `{"phones":["5511988887777"]}`, http.StatusOK, true},
		{"send message", http.MethodPost, instancePath("/messages"), `{"to":"5511988887777","type":"text","text":"hi"}`, http.StatusAccepted, true},
		{"list messages", http.MethodGet, instancePath("/messages"), "", http.StatusOK, true},
		{"get message", http.MethodGet, instancePath("/messages/" + testMessageID.String()), "", http.StatusOK, true},
		{"list chats", http.MethodGet, instancePath("/chats"), "", http.StatusOK, true},
		{"get chat", http.MethodGet, instancePath(chat), "", http.StatusOK, true},
		{"mark read", http.MethodPost, instancePath(chat + "/read"), "", http.StatusOK, true},
		{"presence", http.MethodPost, instancePath(chat + "/presence"), `{"state":"composing"}`, http.StatusNoContent, true},
		{"history", http.MethodPost, instancePath(chat + "/history"), `{"count":5}`, http.StatusAccepted, true},
		{"get media", http.MethodGet, apiPath + "/media/" + testMediaID.String(), "", http.StatusOK, true},
		{"media content", http.MethodGet, apiPath + "/media/" + testMediaID.String() + "/content", "", http.StatusOK, true},
		{"create webhook", http.MethodPost, apiPath + "/webhooks", `{"url":"https://x.test/h"}`, http.StatusCreated, false},
		{"list webhooks", http.MethodGet, apiPath + "/webhooks", "", http.StatusOK, false},
		{"get webhook", http.MethodGet, webhookPath(""), "", http.StatusOK, true},
		{"update webhook", http.MethodPatch, webhookPath(""), `{"active":false}`, http.StatusOK, true},
		{"delete webhook", http.MethodDelete, webhookPath(""), "", http.StatusNoContent, true},
		{"rotate secret", http.MethodPost, webhookPath("/rotate-secret"), "", http.StatusOK, true},
		{"test webhook", http.MethodPost, webhookPath("/test"), "", http.StatusAccepted, true},
		{"list deliveries", http.MethodGet, webhookPath("/deliveries"), "", http.StatusOK, true},
		{"retry delivery", http.MethodPost, webhookPath("/deliveries/" + testDeliveryID.String() + "/retry"), "", http.StatusAccepted, true},
	}
}

func TestEveryRouteRespondsForTheOwner(t *testing.T) {
	router := newBackend().router()
	for _, item := range allRoutes() {
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: item.method, path: item.path, body: item.body, key: ownerKey}.send(t, router)
			if item.status == http.StatusNoContent {
				expectSuccess(t, recorder, item.status)
				return
			}
			if item.name == "media content" {
				if recorder.Code != item.status {
					t.Fatalf("status: got %d", recorder.Code)
				}
				return
			}
			expectSuccess(t, recorder, item.status)
		})
	}
}

func TestEveryRouteRejectsRequestsWithoutKey(t *testing.T) {
	router := newBackend().router()
	for _, item := range allRoutes() {
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: item.method, path: item.path, body: item.body}.send(t, router)
			expectError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestEveryRouteRejectsInvalidKey(t *testing.T) {
	router := newBackend().router()
	for _, item := range allRoutes() {
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: item.method, path: item.path, body: item.body, key: "wrong"}.send(t, router)
			expectError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestScopedRoutesHideResourcesOfOtherClients(t *testing.T) {
	router := newBackend().router()
	for _, item := range allRoutes() {
		if !item.scoped {
			continue
		}
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: item.method, path: item.path, body: item.body, key: otherKey}.send(t, router)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status: got %d, body %s", recorder.Code, recorder.Body.String())
			}
			if decoded := decodeEnvelope(t, recorder); decoded.Success || decoded.Error == nil {
				t.Fatalf("expected error envelope, got %s", recorder.Body.String())
			}
		})
	}
}

func TestInvalidBodiesAreBadRequests(t *testing.T) {
	router := newBackend().router()
	chat := "/chats/" + testChatID.String()
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create instance malformed", http.MethodPost, apiPath + "/instances", `{"name":`},
		{"create instance unknown field", http.MethodPost, apiPath + "/instances", `{"name":"a","bogus":1}`},
		{"create instance wrong type", http.MethodPost, apiPath + "/instances", `{"name":5}`},
		{"create instance bad settings", http.MethodPost, apiPath + "/instances", `{"name":"a","settings":{"sync_history":"yes"}}`},
		{"create instance empty body", http.MethodPost, apiPath + "/instances", ""},
		{"create instance trailing data", http.MethodPost, apiPath + "/instances", `{"name":"a"}{"name":"b"}`},
		{"update instance unknown field", http.MethodPatch, instancePath(""), `{"x":1}`},
		{"pair code malformed", http.MethodPost, instancePath("/pair-code"), `{`},
		{"update settings wrong type", http.MethodPatch, instancePath("/settings"), `{"sync_history":"no"}`},
		{"update settings unknown field", http.MethodPatch, instancePath("/settings"), `{"bogus":true}`},
		{"update settings null field", http.MethodPatch, instancePath("/settings"), `{"sync_history":null}`},
		{"update profile array", http.MethodPatch, instancePath("/profile"), `[]`},
		{"update privacy unknown key", http.MethodPatch, instancePath("/privacy"), `{"bogus":"all"}`},
		{"check contacts malformed", http.MethodPost, instancePath("/contacts/check"), `{`},
		{"send message malformed", http.MethodPost, instancePath("/messages"), `{`},
		{"send message invalid chat id", http.MethodPost, instancePath("/messages"), `{"chat_id":"x","type":"text"}`},
		{"read invalid message id", http.MethodPost, instancePath(chat + "/read"), `{"message_ids":["x"]}`},
		{"presence malformed", http.MethodPost, instancePath(chat + "/presence"), `{`},
		{"history malformed", http.MethodPost, instancePath(chat + "/history"), `{"count":"x"}`},
		{"create webhook malformed", http.MethodPost, apiPath + "/webhooks", `{`},
		{"create webhook invalid instance id", http.MethodPost, apiPath + "/webhooks", `{"url":"https://x.test","instance_ids":["x"]}`},
		{"update webhook malformed", http.MethodPatch, webhookPath(""), `{`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: item.method, path: item.path, body: item.body, key: ownerKey}.send(t, router)
			expectError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
		})
	}
}

func TestInvalidPathAndQueryValuesAreBadRequests(t *testing.T) {
	router := newBackend().router()
	cases := []struct {
		name string
		path string
	}{
		{"instance id", apiPath + "/instances/not-a-uuid"},
		{"chat id", instancePath("/chats/not-a-uuid")},
		{"message id", instancePath("/messages/not-a-uuid")},
		{"media id", apiPath + "/media/not-a-uuid"},
		{"webhook id", apiPath + "/webhooks/not-a-uuid"},
		{"chat filter", instancePath("/messages?chat_id=x")},
		{"since", instancePath("/messages?since=yesterday")},
		{"until", instancePath("/messages?until=2026-13-45")},
		{"active", apiPath + "/webhooks?active=maybe"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			recorder := call{method: http.MethodGet, path: item.path, key: ownerKey}.send(t, router)
			expectError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
		})
	}
}

func TestUnknownRouteReturnsEnvelopeNotFound(t *testing.T) {
	router := newBackend().router()
	recorder := call{method: http.MethodGet, path: "/nowhere", key: ownerKey}.send(t, router)
	expectError(t, recorder, http.StatusNotFound, "NOT_FOUND")
}

func TestRequestIDIsEchoed(t *testing.T) {
	router := newBackend().router()
	recorder := call{method: http.MethodGet, path: apiPath + "/instances", key: ownerKey, headers: map[string]string{"X-Request-Id": "req-1"}}.send(t, router)
	if recorder.Header().Get("X-Request-Id") != "req-1" {
		t.Errorf("request id: got %q", recorder.Header().Get("X-Request-Id"))
	}
}

func TestAPIKeyHeaderAuthenticates(t *testing.T) {
	router := newBackend().router()
	valid := call{method: http.MethodGet, path: apiPath + "/instances", headers: map[string]string{"x-api-key": "external-key"}}.send(t, router)
	expectSuccess(t, valid, http.StatusOK)
	invalid := call{method: http.MethodGet, path: apiPath + "/instances", headers: map[string]string{"x-api-key": "wrong"}}.send(t, router)
	expectError(t, invalid, http.StatusUnauthorized, "UNAUTHORIZED")
}
