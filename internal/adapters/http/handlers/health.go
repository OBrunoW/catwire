package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
)

const (
	readyTimeout   = 3 * time.Second
	statusOK       = "ok"
	statusError    = "error"
	statusDisabled = "disabled"
)

type HealthChecks struct {
	Database func(ctx context.Context) error
	Kafka    func(ctx context.Context) error
}

type HealthHandler struct {
	checks HealthChecks
}

func NewHealthHandler(checks HealthChecks) *HealthHandler {
	return &HealthHandler{checks: checks}
}

type readyResource struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Kafka    string `json:"kafka"`
}

func (h *HealthHandler) Health(w http.ResponseWriter, _ *http.Request) {
	apihttp.WritePlain(w, http.StatusOK, map[string]string{"status": statusOK})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	resource := readyResource{
		Database: checkDependency(ctx, "database", h.checks.Database),
		Kafka:    checkDependency(ctx, "kafka", h.checks.Kafka),
	}
	resource.Status = statusOK
	status := http.StatusOK
	if resource.Database == statusError || resource.Kafka == statusError {
		resource.Status = statusError
		status = http.StatusServiceUnavailable
	}
	apihttp.WritePlain(w, status, resource)
}

func checkDependency(ctx context.Context, name string, check func(ctx context.Context) error) string {
	if check == nil {
		return statusDisabled
	}
	if err := check(ctx); err != nil {
		slog.Error("readiness check failed", "dependency", name, "error", err)
		return statusError
	}
	return statusOK
}
