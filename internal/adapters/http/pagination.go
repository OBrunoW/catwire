package http

import (
	"net/http"
	"strconv"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

type PageParams struct {
	Limit  int
	Page   int
	Cursor string
}

func (p PageParams) UsesPage() bool {
	return p.Page > 0
}

func (p PageParams) Offset() int {
	if !p.UsesPage() {
		return 0
	}
	return (p.Page - 1) * p.Limit
}

type PageResult[T any] struct {
	Data       []T     `json:"data"`
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

func ParsePageParams(r *http.Request) PageParams {
	query := r.URL.Query()
	params := PageParams{Limit: parseLimit(query.Get("limit"), query.Get("per_page"))}
	if page, err := strconv.Atoi(query.Get("page")); err == nil && page > 0 {
		params.Page = page
		return params
	}
	params.Cursor = query.Get("cursor")
	return params
}

func parseLimit(values ...string) int {
	for _, raw := range values {
		if raw == "" {
			continue
		}
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return defaultLimit
		}
		return min(limit, maxLimit)
	}
	return defaultLimit
}

func BuildPage[T any](rows []T, limit int, nextCursor func(last T) string) PageResult[T] {
	result := PageResult[T]{Data: rows, Limit: limit}
	if len(rows) <= limit {
		if result.Data == nil {
			result.Data = []T{}
		}
		return result
	}
	result.Data = rows[:limit]
	result.HasMore = true
	cursor := nextCursor(result.Data[limit-1])
	result.NextCursor = &cursor
	return result
}
