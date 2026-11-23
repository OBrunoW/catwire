package http_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

func requestWithQuery(query string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/items?"+query, nil)
}

func TestParsePageParamsLimit(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"default when absent", "", 50},
		{"explicit limit", "limit=20", 20},
		{"maximum accepted", "limit=100", 100},
		{"above maximum is capped", "limit=500", 100},
		{"zero falls back to default", "limit=0", 50},
		{"negative falls back to default", "limit=-5", 50},
		{"non numeric falls back to default", "limit=abc", 50},
		{"per_page alias", "per_page=30", 30},
		{"limit wins over per_page", "limit=10&per_page=30", 10},
		{"per_page capped", "per_page=1000", 100},
		{"invalid limit does not fall to per_page", "limit=abc&per_page=30", 50},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := apihttp.ParsePageParams(requestWithQuery(test.query))
			if params.Limit != test.expected {
				t.Errorf("limit: got %d, expected %d", params.Limit, test.expected)
			}
		})
	}
}

func TestParsePageParamsPageMode(t *testing.T) {
	params := apihttp.ParsePageParams(requestWithQuery("page=3&limit=20&cursor=abc"))
	if !params.UsesPage() || params.Page != 3 {
		t.Fatalf("expected page mode, got %+v", params)
	}
	if params.Cursor != "" {
		t.Errorf("cursor must be ignored in page mode, got %q", params.Cursor)
	}
	if params.Offset() != 40 {
		t.Errorf("offset: got %d", params.Offset())
	}
}

func TestParsePageParamsCursorMode(t *testing.T) {
	params := apihttp.ParsePageParams(requestWithQuery("cursor=abc"))
	if params.UsesPage() || params.Cursor != "abc" || params.Offset() != 0 {
		t.Errorf("unexpected params: %+v", params)
	}
}

func TestParsePageParamsInvalidPageFallsBackToCursor(t *testing.T) {
	for _, query := range []string{"page=0&cursor=abc", "page=-1&cursor=abc", "page=x&cursor=abc"} {
		t.Run(query, func(t *testing.T) {
			params := apihttp.ParsePageParams(requestWithQuery(query))
			if params.UsesPage() || params.Cursor != "abc" {
				t.Errorf("unexpected params: %+v", params)
			}
		})
	}
}

func TestBuildPageTrimsExtraRow(t *testing.T) {
	rows := []int{1, 2, 3, 4}
	page := apihttp.BuildPage(rows, 3, func(last int) string { return "after-" + strconv.Itoa(last) })
	if len(page.Data) != 3 || page.Data[2] != 3 {
		t.Errorf("data: got %v", page.Data)
	}
	if !page.HasMore || page.Limit != 3 {
		t.Errorf("page: %+v", page)
	}
	if page.NextCursor == nil || *page.NextCursor != "after-3" {
		t.Errorf("next cursor: got %v", page.NextCursor)
	}
}

func TestBuildPageWithoutMoreRows(t *testing.T) {
	tests := []struct {
		name string
		rows []int
	}{
		{"fewer rows than limit", []int{1, 2}},
		{"exactly limit rows", []int{1, 2, 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page := apihttp.BuildPage(test.rows, 3, func(int) string { return "unused" })
			if page.HasMore || page.NextCursor != nil {
				t.Errorf("page: %+v", page)
			}
			if len(page.Data) != len(test.rows) {
				t.Errorf("data: got %v", page.Data)
			}
		})
	}
}

func TestBuildPageNilRowsBecomesEmptySlice(t *testing.T) {
	page := apihttp.BuildPage[int](nil, 10, func(int) string { return "" })
	if page.Data == nil || len(page.Data) != 0 {
		t.Errorf("data: got %#v", page.Data)
	}
}

type cursorPosition struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
}

func TestCursorRoundTrip(t *testing.T) {
	original := cursorPosition{ID: "abc-123", CreatedAt: 1700000000}
	encoded, err := apihttp.EncodeCursor(original)
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}
	if encoded == "" {
		t.Fatal("expected non empty cursor")
	}
	var decoded cursorPosition
	if err := apihttp.DecodeCursor(encoded, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != original {
		t.Errorf("got %+v, expected %+v", decoded, original)
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	tests := []struct {
		name   string
		cursor string
	}{
		{"not base64", "%%%not-base64"},
		{"base64 of non json", "bm90LWpzb24"},
		{"empty cursor", ""},
		{"json of wrong shape", "WzEsMl0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded cursorPosition
			err := apihttp.DecodeCursor(test.cursor, &decoded)
			appErr, ok := domain.AsAppError(err)
			if !ok {
				t.Fatalf("expected AppError, got %v", err)
			}
			if appErr.Status != http.StatusBadRequest || appErr.Code != "BAD_REQUEST" {
				t.Errorf("got %+v", appErr)
			}
		})
	}
}
