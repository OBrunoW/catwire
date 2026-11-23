package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OBrunoW/catwire/internal/config"
)

type errorResponse struct {
	Success bool `json:"success"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func authConfig() config.Config {
	return config.Config{
		AppEnv:      "production",
		ServiceKeys: []config.ServiceKey{{Name: "crm", Key: "service-secret"}, {Name: "platform", Key: "platform-secret"}},
		APIKey:      "api-secret",
	}
}

func callAuth(cfg config.Config, headers map[string]string) (*httptest.ResponseRecorder, string) {
	capturedClientID := ""
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClientID = ClientID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder, capturedClientID
}

func TestAuthAccepts(t *testing.T) {
	tests := []struct {
		name             string
		cfg              config.Config
		headers          map[string]string
		expectedClientID string
	}{
		{"first service key", authConfig(), map[string]string{"X-Service-Key": "service-secret"}, "crm"},
		{"second service key", authConfig(), map[string]string{"X-Service-Key": "platform-secret"}, "platform"},
		{"api key", authConfig(), map[string]string{"x-api-key": "api-secret"}, "api"},
		{"development without keys", config.Config{AppEnv: "development"}, nil, "dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, clientID := callAuth(test.cfg, test.headers)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status: got %d", recorder.Code)
			}
			if clientID != test.expectedClientID {
				t.Errorf("client id: got %q, expected %q", clientID, test.expectedClientID)
			}
		})
	}
}

func TestAuthRejects(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		headers map[string]string
	}{
		{"missing credentials", authConfig(), nil},
		{"wrong service key", authConfig(), map[string]string{"X-Service-Key": "nope"}},
		{"wrong api key", authConfig(), map[string]string{"x-api-key": "nope"}},
		{"service key sent as api key", authConfig(), map[string]string{"x-api-key": "service-secret"}},
		{"api key sent as service key", authConfig(), map[string]string{"X-Service-Key": "api-secret"}},
		{"api key header without configured api key", config.Config{AppEnv: "production", ServiceKeys: authConfig().ServiceKeys}, map[string]string{"x-api-key": ""}},
		{"production without keys", config.Config{AppEnv: "production"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, clientID := callAuth(test.cfg, test.headers)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status: got %d", recorder.Code)
			}
			if clientID != "" {
				t.Errorf("handler should not run, got client id %q", clientID)
			}
			var body errorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if body.Success || body.Error.Code != "UNAUTHORIZED" || body.Error.Message == "" {
				t.Errorf("unexpected body: %+v", body)
			}
		})
	}
}

func TestClientIDWithoutValueIsEmpty(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ClientID(request.Context()); got != "" {
		t.Errorf("got %q", got)
	}
}
