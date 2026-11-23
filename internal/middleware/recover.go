package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type headerTracker struct {
	http.ResponseWriter
	wroteHeader bool
}

func (t *headerTracker) WriteHeader(status int) {
	t.wroteHeader = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *headerTracker) Write(body []byte) (int, error) {
	t.wroteHeader = true
	return t.ResponseWriter.Write(body)
}

func (t *headerTracker) Unwrap() http.ResponseWriter {
	return t.ResponseWriter
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracker := &headerTracker{ResponseWriter: w}
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			slog.Error("request panicked", "panic", fmt.Sprint(recovered), "method", r.Method, "path", r.URL.Path, "stack", string(debug.Stack()))
			if tracker.wroteHeader {
				slog.Error("response already started, envelope skipped", "method", r.Method, "path", r.URL.Path)
				return
			}
			apihttp.WriteError(w, domain.Internal())
		}()
		next.ServeHTTP(tracker, r)
	})
}
