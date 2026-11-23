package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OBrunoW/catwire/internal/config"
	"github.com/OBrunoW/catwire/internal/domain"
	"github.com/OBrunoW/catwire/internal/middleware"
)

const (
	ownerKey = "crm-key"
	otherKey = "other-key"
	apiPath  = "/api/v1"
)

type backend struct {
	instances *fakeInstances
	sessions  *fakeSessions
	settings  *fakeSettings
	contacts  *fakeContacts
	sender    *fakeSender
	messages  *fakeMessageQueries
	chats     *fakeChatQueries
	reads     *fakeReads
	presence  *fakePresence
	history   *fakeHistory
	profiles  *fakeProfiles
	privacy   *fakePrivacy
	library   *fakeLibrary
	content   *fakeContent
	webhooks  *fakeWebhooks
	checks    HealthChecks
	signed    bool
	maxMedia  int64
}

func newBackend() *backend {
	mimeType := "application/pdf"
	fileName := "contract.pdf"
	return &backend{
		instances: &fakeInstances{},
		sessions:  &fakeSessions{},
		settings:  &fakeSettings{},
		contacts:  &fakeContacts{},
		sender:    &fakeSender{},
		messages:  &fakeMessageQueries{},
		chats:     &fakeChatQueries{},
		reads:     &fakeReads{},
		presence:  &fakePresence{},
		history:   &fakeHistory{},
		profiles:  &fakeProfiles{},
		privacy:   &fakePrivacy{},
		library:   &fakeLibrary{},
		content: &fakeContent{
			signature: "good",
			media:     domain.Media{Kind: "document", MimeType: &mimeType, FileName: &fileName},
		},
		webhooks: &fakeWebhooks{},
		signed:   true,
		maxMedia: 1024,
	}
}

func (b *backend) router() http.Handler {
	cfg := config.Config{
		AppEnv:      "production",
		APIKey:      "external-key",
		ServiceKeys: []config.ServiceKey{{Name: testOwner, Key: ownerKey}, {Name: "other", Key: otherKey}},
	}
	return NewRouter(Dependencies{
		Auth:              middleware.Auth(cfg),
		Instances:         b.instances,
		Sessions:          b.sessions,
		Settings:          b.settings,
		Contacts:          b.contacts,
		Sender:            b.sender,
		MessageQueries:    b.messages,
		ChatQueries:       b.chats,
		Reads:             b.reads,
		Presence:          b.presence,
		History:           b.history,
		Profiles:          b.profiles,
		Privacy:           b.privacy,
		Library:           b.library,
		Content:           b.content,
		SignedMediaAccess: b.signed,
		Webhooks:          b.webhooks,
		Health:            b.checks,
		MaxMediaBytes:     b.maxMedia,
		MessageBodyLimit:  b.maxMedia * 2,
	})
}

type call struct {
	method  string
	path    string
	body    string
	key     string
	headers map[string]string
}

func (c call) send(t *testing.T, router http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if c.body != "" {
		reader = strings.NewReader(c.body)
	}
	request := httptest.NewRequest(c.method, c.path, reader)
	if c.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		request.Header.Set("X-Service-Key", c.key)
	}
	for name, value := range c.headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var decoded envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("body is not json: %v: %s", err, recorder.Body.String())
	}
	return decoded
}

func expectSuccess(t *testing.T, recorder *httptest.ResponseRecorder, status int) envelope {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status: got %d, expected %d, body %s", recorder.Code, status, recorder.Body.String())
	}
	if status == http.StatusNoContent {
		return envelope{}
	}
	decoded := decodeEnvelope(t, recorder)
	if !decoded.Success || decoded.Error != nil {
		t.Fatalf("expected success envelope, got %s", recorder.Body.String())
	}
	return decoded
}

func expectError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status: got %d, expected %d, body %s", recorder.Code, status, recorder.Body.String())
	}
	decoded := decodeEnvelope(t, recorder)
	if decoded.Success || decoded.Error == nil || decoded.Error.Code != code {
		t.Fatalf("expected error %s, got %s", code, recorder.Body.String())
	}
}

func unmarshalData(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("data is not an object: %v: %s", err, data)
	}
	return decoded
}

var errDatabaseDown = errors.New("database down")
