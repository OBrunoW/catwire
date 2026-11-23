package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func healthy(context.Context) error { return nil }

func failing(context.Context) error { return errDatabaseDown }

func plainBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	return body
}

func TestHealthIsPlainWithoutAuth(t *testing.T) {
	recorder := call{method: http.MethodGet, path: "/health"}.send(t, newBackend().router())
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(plainBody(t, recorder), map[string]any{"status": "ok"}) {
		t.Fatalf("status %d body %s", recorder.Code, recorder.Body.String())
	}
}

func TestReadyReportsEveryDependency(t *testing.T) {
	cases := []struct {
		name     string
		checks   HealthChecks
		status   int
		expected map[string]any
	}{
		{"all ok", HealthChecks{Database: healthy, Kafka: healthy}, http.StatusOK, map[string]any{"status": "ok", "database": "ok", "kafka": "ok"}},
		{"kafka disabled", HealthChecks{Database: healthy}, http.StatusOK, map[string]any{"status": "ok", "database": "ok", "kafka": "disabled"}},
		{"database down", HealthChecks{Database: failing, Kafka: healthy}, http.StatusServiceUnavailable, map[string]any{"status": "error", "database": "error", "kafka": "ok"}},
		{"kafka down", HealthChecks{Database: healthy, Kafka: failing}, http.StatusServiceUnavailable, map[string]any{"status": "error", "database": "ok", "kafka": "error"}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			backend := newBackend()
			backend.checks = item.checks
			recorder := call{method: http.MethodGet, path: "/ready"}.send(t, backend.router())
			if recorder.Code != item.status {
				t.Fatalf("status: got %d", recorder.Code)
			}
			body := plainBody(t, recorder)
			if !reflect.DeepEqual(body, item.expected) {
				t.Errorf("body: got %v", body)
			}
			if _, wrapped := body["success"]; wrapped {
				t.Error("ready must not use the envelope")
			}
		})
	}
}

func TestReadyDoesNotLeakFailureDetails(t *testing.T) {
	backend := newBackend()
	backend.checks = HealthChecks{Database: failing}
	recorder := call{method: http.MethodGet, path: "/ready"}.send(t, backend.router())
	if body := recorder.Body.String(); strings.Contains(body, "database down") {
		t.Errorf("failure detail leaked: %s", body)
	}
}
