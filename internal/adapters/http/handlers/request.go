package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
	"github.com/OBrunoW/catwire/internal/middleware"
)

const defaultBodyLimit int64 = 1 << 20

func clientID(r *http.Request) string {
	return middleware.ClientID(r.Context())
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, domain.BadRequest(name + " must be a valid uuid")
	}
	return parsed, nil
}

func queryUUID(r *http.Request, name string) (*uuid.UUID, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, domain.BadRequest(name + " must be a valid uuid")
	}
	return &parsed, nil
}

func queryTime(r *http.Request, name string) (*time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, domain.BadRequest(name + " must be an ISO 8601 timestamp")
	}
	return &parsed, nil
}

func queryBool(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, domain.BadRequest(name + " must be true or false")
	}
	return &parsed, nil
}

type bodyOptions struct {
	limit    int64
	optional bool
	tooLarge *domain.AppError
}

func decodeBody(w http.ResponseWriter, r *http.Request, destination any, options bodyOptions) error {
	if options.limit <= 0 {
		options.limit = defaultBodyLimit
	}
	r.Body = http.MaxBytesReader(w, r.Body, options.limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return classifyBodyError(err, options)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return classifyBodyError(err, options)
	}
	return nil
}

func classifyBodyError(err error, options bodyOptions) error {
	if appErr, ok := domain.AsAppError(err); ok {
		return appErr
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		if options.tooLarge != nil {
			return options.tooLarge
		}
		return domain.BadRequest("request body is too large")
	}
	if errors.Is(err, io.EOF) {
		if options.optional {
			return nil
		}
		return domain.BadRequest("request body is required")
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return domain.BadRequest("invalid value for field " + typeErr.Field)
	}
	return domain.BadRequest("request body is not valid json")
}

func mapPage[S, T any](page apihttp.PageResult[S], convert func(S) T) apihttp.PageResult[T] {
	rows := make([]T, 0, len(page.Data))
	for _, row := range page.Data {
		rows = append(rows, convert(row))
	}
	return apihttp.PageResult[T]{Data: rows, Limit: page.Limit, HasMore: page.HasMore, NextCursor: page.NextCursor}
}
