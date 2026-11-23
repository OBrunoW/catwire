package middleware

import (
	"net/http"

	"github.com/google/uuid"
)

const (
	requestIDHeader    = "X-Request-Id"
	maxRequestIDLength = 128
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		if !validRequestID(requestID) {
			requestID = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r)
	})
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	for _, char := range value {
		if !isRequestIDChar(char) {
			return false
		}
	}
	return true
}

func isRequestIDChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return true
	}
	return char == '-' || char == '_' || char == '.'
}
