package http_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	return body
}

func TestWriteDataWrapsPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	apihttp.WriteData(recorder, http.StatusCreated, map[string]string{"id": "abc"})
	if recorder.Code != http.StatusCreated {
		t.Errorf("status: got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("content type: got %q", contentType)
	}
	expected := map[string]any{"success": true, "data": map[string]any{"id": "abc"}}
	if got := decodeBody(t, recorder); !reflect.DeepEqual(got, expected) {
		t.Errorf("body: got %v", got)
	}
}

func TestStatusHelpers(t *testing.T) {
	tests := []struct {
		name     string
		write    func(http.ResponseWriter, any)
		expected int
	}{
		{"ok", apihttp.OK, http.StatusOK},
		{"created", apihttp.Created, http.StatusCreated},
		{"accepted", apihttp.Accepted, http.StatusAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.write(recorder, "value")
			if recorder.Code != test.expected {
				t.Errorf("status: got %d", recorder.Code)
			}
			if decodeBody(t, recorder)["success"] != true {
				t.Error("expected success true")
			}
		})
	}
}

func TestNoContentHasEmptyBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	apihttp.NoContent(recorder)
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Errorf("status %d, body %q", recorder.Code, recorder.Body.String())
	}
}

func TestWriteErrorWithAppError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode string
		expectedHTTP int
	}{
		{"not found", domain.InstanceNotFound(), "INSTANCE_NOT_FOUND", http.StatusNotFound},
		{"bad request", domain.BadRequest("bad input"), "BAD_REQUEST", http.StatusBadRequest},
		{"conflict", domain.IdempotencyConflict(), "IDEMPOTENCY_CONFLICT", http.StatusConflict},
		{"wrapped app error", errors.Join(errors.New("context"), domain.QRExpired()), "QR_EXPIRED", http.StatusGone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			apihttp.WriteError(recorder, test.err)
			if recorder.Code != test.expectedHTTP {
				t.Errorf("status: got %d", recorder.Code)
			}
			body := decodeBody(t, recorder)
			detail, ok := body["error"].(map[string]any)
			if body["success"] != false || !ok {
				t.Fatalf("unexpected body: %v", body)
			}
			if detail["code"] != test.expectedCode || detail["message"] == "" {
				t.Errorf("detail: got %v", detail)
			}
			if _, hasData := body["data"]; hasData {
				t.Error("error body must not carry data")
			}
		})
	}
}

func TestWriteErrorWithUnknownErrorIsInternal(t *testing.T) {
	recorder := httptest.NewRecorder()
	apihttp.WriteError(recorder, errors.New("database exploded"))
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d", recorder.Code)
	}
	detail := decodeBody(t, recorder)["error"].(map[string]any)
	if detail["code"] != "INTERNAL_ERROR" || detail["message"] != "internal error" {
		t.Errorf("detail: got %v", detail)
	}
}

func TestWritePlainHasNoEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	apihttp.WritePlain(recorder, http.StatusOK, map[string]string{"status": "ok"})
	expected := map[string]any{"status": "ok"}
	if got := decodeBody(t, recorder); !reflect.DeepEqual(got, expected) {
		t.Errorf("body: got %v", got)
	}
}
