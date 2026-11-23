package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestHealthAndReadyRespondWithoutEnvelope(t *testing.T) {
	router := newRouter()
	for _, path := range []string{"/health", "/ready"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status: got %d", recorder.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if !reflect.DeepEqual(body, map[string]any{"status": "ok"}) {
				t.Errorf("body: got %v", body)
			}
		})
	}
}
