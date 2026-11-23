package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/config"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	serviceKeyHeader = "X-Service-Key"
	apiKeyHeader     = "x-api-key"
	apiClientID      = "api"
	developmentID    = "dev"
)

type clientIDContextKey struct{}

func ClientID(ctx context.Context) string {
	clientID, _ := ctx.Value(clientIDContextKey{}).(string)
	return clientID
}

func WithClientID(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, clientIDContextKey{}, clientID)
}

func Auth(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientID, ok := resolveClientID(cfg, r)
			if !ok {
				apihttp.WriteError(w, domain.Unauthorized("missing or invalid credentials"))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithClientID(r.Context(), clientID)))
		})
	}
}

func resolveClientID(cfg config.Config, r *http.Request) (string, bool) {
	if !cfg.AuthConfigured() {
		return developmentID, cfg.IsDevelopment()
	}
	if provided := r.Header.Get(serviceKeyHeader); provided != "" {
		if name, ok := matchServiceKey(cfg.ServiceKeys, provided); ok {
			return name, true
		}
	}
	if provided := r.Header.Get(apiKeyHeader); provided != "" && cfg.APIKey != "" {
		if equalConstantTime(cfg.APIKey, provided) {
			return apiClientID, true
		}
	}
	return "", false
}

func matchServiceKey(keys []config.ServiceKey, provided string) (string, bool) {
	matchedName := ""
	matched := false
	for _, candidate := range keys {
		if equalConstantTime(candidate.Key, provided) && !matched {
			matchedName = candidate.Name
			matched = true
		}
	}
	return matchedName, matched
}

func equalConstantTime(expected, provided string) bool {
	expectedSum := sha256.Sum256([]byte(expected))
	providedSum := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(expectedSum[:], providedSum[:]) == 1
}
