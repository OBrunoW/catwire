package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/OBrunoW/catwire/internal/domain"
)

type successBody struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Success bool        `json:"success"`
	Error   errorDetail `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}

func WriteData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, successBody{Success: true, Data: data})
}

func OK(w http.ResponseWriter, data any) {
	WriteData(w, http.StatusOK, data)
}

func Created(w http.ResponseWriter, data any) {
	WriteData(w, http.StatusCreated, data)
}

func Accepted(w http.ResponseWriter, data any) {
	WriteData(w, http.StatusAccepted, data)
}

func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func WriteError(w http.ResponseWriter, err error) {
	appErr, ok := domain.AsAppError(err)
	if !ok {
		slog.Error("unexpected error", "error", err)
		appErr = domain.Internal()
	}
	writeJSON(w, appErr.Status, errorBody{
		Success: false,
		Error:   errorDetail{Code: appErr.Code, Message: appErr.Message},
	})
}

func WritePlain(w http.ResponseWriter, status int, body any) {
	writeJSON(w, status, body)
}
