package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverWritesInternalErrorEnvelope(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"INTERNAL_ERROR"`) || strings.Contains(body, "boom") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestRecoverAfterHeaderWrittenSkipsEnvelope(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("late boom")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status: got %d", recorder.Code)
	}
	if recorder.Body.String() != "partial" {
		t.Errorf("unexpected body: %s", recorder.Body.String())
	}
}
