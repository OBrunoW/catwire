package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDEchoesValidHeaderAndReplacesInvalid(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name     string
		provided string
		keep     bool
	}{
		{"valid", "req-123.abc_9", true},
		{"missing", "", false},
		{"with space", "bad id", false},
		{"too long", string(make([]byte, 200)), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.provided != "" {
				request.Header.Set("X-Request-Id", test.provided)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			got := recorder.Header().Get("X-Request-Id")
			if got == "" {
				t.Fatal("missing X-Request-Id in response")
			}
			if test.keep && got != test.provided {
				t.Errorf("expected %q echoed, got %q", test.provided, got)
			}
			if !test.keep && got == test.provided {
				t.Errorf("expected replacement of %q", test.provided)
			}
		})
	}
}
