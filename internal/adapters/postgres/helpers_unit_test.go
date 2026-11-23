package postgres

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChatCursorClause(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name         string
		position     optionalTimePosition
		expectedSQL  string
		expectedArgs []any
	}{
		{
			name:         "null timestamp",
			position:     optionalTimePosition{ID: id},
			expectedSQL:  "(c.last_message_at IS NULL AND c.id < $1)",
			expectedArgs: []any{id},
		},
		{
			name:         "set timestamp",
			position:     optionalTimePosition{At: &at, ID: id},
			expectedSQL:  "(c.last_message_at IS NULL OR c.last_message_at < $2 OR (c.last_message_at = $2 AND c.id < $1))",
			expectedArgs: []any{id, at},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &queryBuilder{}

			clause := chatCursorClause(builder, test.position)

			if clause != test.expectedSQL {
				t.Fatalf("expected %q, got %q", test.expectedSQL, clause)
			}
			if !reflect.DeepEqual(builder.args, test.expectedArgs) {
				t.Fatalf("expected args %v, got %v", test.expectedArgs, builder.args)
			}
		})
	}
}

func TestQueryBuilder(t *testing.T) {
	tests := []struct {
		name          string
		clauses       []string
		values        []any
		expectedWhere string
		expectedPlace []string
	}{
		{name: "empty", expectedWhere: ""},
		{
			name:          "one clause",
			clauses:       []string{"a = $1"},
			values:        []any{1},
			expectedWhere: " WHERE a = $1",
			expectedPlace: []string{"$1"},
		},
		{
			name:          "two clauses",
			clauses:       []string{"a = $1", "b = $2"},
			values:        []any{1, "x"},
			expectedWhere: " WHERE a = $1 AND b = $2",
			expectedPlace: []string{"$1", "$2"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &queryBuilder{}
			placeholders := []string{}
			for _, value := range test.values {
				placeholders = append(placeholders, builder.arg(value))
			}
			for _, clause := range test.clauses {
				builder.where(clause)
			}

			if builder.whereClause() != test.expectedWhere {
				t.Fatalf("expected %q, got %q", test.expectedWhere, builder.whereClause())
			}
			if len(placeholders) != len(test.expectedPlace) {
				t.Fatalf("expected placeholders %v, got %v", test.expectedPlace, placeholders)
			}
			for index := range placeholders {
				if placeholders[index] != test.expectedPlace[index] {
					t.Fatalf("expected placeholders %v, got %v", test.expectedPlace, placeholders)
				}
			}
			if len(builder.args) != len(test.values) {
				t.Fatalf("expected %d args, got %d", len(test.values), len(builder.args))
			}
		})
	}
}

func TestEscapeLikeAndLikePattern(t *testing.T) {
	tests := []struct {
		name            string
		term            string
		expectedEscaped string
		expectedPattern string
	}{
		{name: "plain", term: "abc", expectedEscaped: "abc", expectedPattern: "%abc%"},
		{name: "percent", term: "50%", expectedEscaped: `50\%`, expectedPattern: `%50\%%`},
		{name: "underscore", term: "a_b", expectedEscaped: `a\_b`, expectedPattern: `%a\_b%`},
		{name: "backslash", term: `a\b`, expectedEscaped: `a\\b`, expectedPattern: `%a\\b%`},
		{name: "empty", term: "", expectedEscaped: "", expectedPattern: "%%"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if escaped := escapeLike(test.term); escaped != test.expectedEscaped {
				t.Fatalf("escapeLike: expected %q, got %q", test.expectedEscaped, escaped)
			}
			if pattern := likePattern(test.term); pattern != test.expectedPattern {
				t.Fatalf("likePattern: expected %q, got %q", test.expectedPattern, pattern)
			}
		})
	}
}

func TestOrEmptyJSON(t *testing.T) {
	tests := []struct {
		name     string
		raw      []byte
		fallback string
		expected string
	}{
		{name: "nil", raw: nil, fallback: "{}", expected: "{}"},
		{name: "empty", raw: []byte{}, fallback: "[]", expected: "[]"},
		{name: "filled", raw: []byte(`{"a":1}`), fallback: "{}", expected: `{"a":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := string(orEmptyJSON(test.raw, test.fallback)); got != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, got)
			}
		})
	}
}

func TestOrEmptyStrings(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "nil", values: nil},
		{name: "filled", values: []string{"a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := orEmptyStrings(test.values)

			if got == nil {
				t.Fatal("expected non-nil slice")
			}
			if len(got) != len(test.values) {
				t.Fatalf("expected %d items, got %d", len(test.values), len(got))
			}
		})
	}
}

func TestOrEmptyUUIDs(t *testing.T) {
	tests := []struct {
		name   string
		values []uuid.UUID
	}{
		{name: "nil", values: nil},
		{name: "filled", values: []uuid.UUID{uuid.New()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := orEmptyUUIDs(test.values)

			if got == nil {
				t.Fatal("expected non-nil slice")
			}
			if len(got) != len(test.values) {
				t.Fatalf("expected %d items, got %d", len(test.values), len(got))
			}
		})
	}
}
