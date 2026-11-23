package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const uniqueViolationCode = "23505"

var (
	ErrIdempotencyKeyTaken  = errors.New("postgres: idempotency key already used in this instance")
	ErrChatIdentityConflict = domain.ErrChatIdentityConflict
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = pgx.Tx(nil)
)

type scanner interface {
	Scan(dest ...any) error
}

func WithTx(ctx context.Context, pool *pgxpool.Pool, run func(tx DBTX) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres begin: %w", err)
	}
	if err := run(tx); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("postgres rollback: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres commit: %w", err)
	}
	return nil
}

func uniqueConstraint(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return pgErr.ConstraintName, true
	}
	return "", false
}

func wrapError(operation string, err error) error {
	return fmt.Errorf("postgres %s: %w", operation, err)
}

func collect[T any](rows pgx.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func qualify(prefix, columns string) string {
	parts := strings.Split(columns, ", ")
	for index, column := range parts {
		parts[index] = prefix + "." + column
	}
	return strings.Join(parts, ", ")
}

type queryBuilder struct {
	args    []any
	clauses []string
}

func (b *queryBuilder) arg(value any) string {
	b.args = append(b.args, value)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *queryBuilder) where(clause string) {
	b.clauses = append(b.clauses, clause)
}

func (b *queryBuilder) whereClause() string {
	if len(b.clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(b.clauses, " AND ")
}

func escapeLike(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
}

func likePattern(term string) string {
	return "%" + escapeLike(term) + "%"
}

type timePosition struct {
	At time.Time `json:"at"`
	ID uuid.UUID `json:"id"`
}

type optionalTimePosition struct {
	At *time.Time `json:"at"`
	ID uuid.UUID  `json:"id"`
}

func decodeCursor(params apihttp.PageParams, position any) (bool, error) {
	if params.UsesPage() || params.Cursor == "" {
		return false, nil
	}
	if err := apihttp.DecodeCursor(params.Cursor, position); err != nil {
		return false, err
	}
	return true, nil
}

func queryPage[T any](
	ctx context.Context,
	db DBTX,
	builder *queryBuilder,
	baseSQL string,
	orderBy string,
	params apihttp.PageParams,
	scan func(scanner) (T, error),
	position func(T) any,
) (apihttp.PageResult[T], error) {
	limitArg := builder.arg(params.Limit + 1)
	offsetArg := builder.arg(params.Offset())
	statement := baseSQL + builder.whereClause() + " ORDER BY " + orderBy + " LIMIT " + limitArg + " OFFSET " + offsetArg
	rows, err := db.Query(ctx, statement, builder.args...)
	if err != nil {
		return apihttp.PageResult[T]{}, wrapError("list", err)
	}
	items, err := collect(rows, scan)
	if err != nil {
		return apihttp.PageResult[T]{}, wrapError("list", err)
	}
	return buildPage(items, params.Limit, position)
}

func buildPage[T any](items []T, limit int, position func(T) any) (apihttp.PageResult[T], error) {
	var encodeErr error
	page := apihttp.BuildPage(items, limit, func(last T) string {
		cursor, err := apihttp.EncodeCursor(position(last))
		if err != nil {
			encodeErr = err
		}
		return cursor
	})
	if encodeErr != nil {
		return apihttp.PageResult[T]{}, wrapError("encode cursor", encodeErr)
	}
	return page, nil
}

func orEmptyJSON(raw []byte, fallback string) []byte {
	if len(raw) == 0 {
		return []byte(fallback)
	}
	return raw
}

func orEmptyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func orEmptyUUIDs(values []uuid.UUID) []uuid.UUID {
	if values == nil {
		return []uuid.UUID{}
	}
	return values
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
