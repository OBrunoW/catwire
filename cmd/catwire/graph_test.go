package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/whatsapp"
	"github.com/OBrunoW/catwire/internal/config"
)

const (
	assemblyKey      = "assembly-key"
	assemblyInstance = "0193a1f2-7c1e-7b8a-9d4e-1f2a3b4c5d6e"
	assemblyChat     = "0193a1f3-0000-7000-8000-0000000000c1"
	assemblyWebhook  = "0193a1f4-0000-7000-8000-0000000000b1"
)

func assembledRouter(t *testing.T) http.Handler {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://catwire:secret@127.0.0.1:1/catwire?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	cfg := config.Config{
		AppEnv:        "production",
		ServiceKeys:   []config.ServiceKey{{Name: "crm", Key: assemblyKey}},
		MediaDir:      t.TempDir(),
		MediaMaxBytes: 1 << 20,
		FFmpegPath:    "ffmpeg",
	}
	built, err := assemble(cfg, pool, &whatsapp.Store{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return built.handler
}

func send(router http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		request.Header.Set("X-Service-Key", key)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAssembledRouterRequiresAuthOnEveryResource(t *testing.T) {
	router := assembledRouter(t)
	instance := "/api/v1/instances/" + assemblyInstance
	paths := []string{
		"/api/v1/instances", instance, instance + "/settings", instance + "/profile", instance + "/privacy",
		instance + "/messages", instance + "/chats", instance + "/chats/" + assemblyChat,
		"/api/v1/webhooks", "/api/v1/webhooks/" + assemblyWebhook, "/api/v1/media/" + assemblyChat,
		"/api/v1/media/" + assemblyChat + "/content",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			recorder := send(router, http.MethodGet, path, "", "")
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status: got %d, body %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAssembledRouterMountsEveryRouteBehindValidation(t *testing.T) {
	router := assembledRouter(t)
	instance := "/api/v1/instances/" + assemblyInstance
	chat := instance + "/chats/" + assemblyChat
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/instances"},
		{http.MethodPatch, instance},
		{http.MethodPatch, instance + "/settings"},
		{http.MethodPatch, instance + "/profile"},
		{http.MethodPatch, instance + "/privacy"},
		{http.MethodPost, instance + "/contacts/check"},
		{http.MethodPost, instance + "/messages"},
		{http.MethodPost, chat + "/read"},
		{http.MethodPost, chat + "/presence"},
		{http.MethodPost, chat + "/history"},
		{http.MethodPost, "/api/v1/webhooks"},
		{http.MethodPatch, "/api/v1/webhooks/" + assemblyWebhook},
	}
	for _, item := range cases {
		t.Run(item.method+" "+item.path, func(t *testing.T) {
			recorder := send(router, item.method, item.path, `{"broken"`, assemblyKey)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, body %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAssembledRouterRoutesWithoutBodyReachTheServices(t *testing.T) {
	router := assembledRouter(t)
	for _, path := range []string{"/api/v1/instances/not-a-uuid", "/api/v1/webhooks/not-a-uuid", "/api/v1/media/not-a-uuid"} {
		recorder := send(router, http.MethodGet, path, "", assemblyKey)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", path, recorder.Code)
		}
	}
	missing := send(router, http.MethodGet, "/api/v1/unknown", "", assemblyKey)
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown route: got %d", missing.Code)
	}
}

func TestAssembledHealthAndReady(t *testing.T) {
	router := assembledRouter(t)
	health := send(router, http.MethodGet, "/health", "", "")
	if health.Code != http.StatusOK || strings.TrimSpace(health.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("health: %d %s", health.Code, health.Body.String())
	}
	ready := send(router, http.MethodGet, "/ready", "", "")
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status: got %d", ready.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(ready.Body.Bytes(), &body); err != nil {
		t.Fatalf("ready body: %v", err)
	}
	if body["database"] != "error" || body["kafka"] != "disabled" || body["status"] != "error" {
		t.Errorf("unexpected ready body: %v", body)
	}
}
