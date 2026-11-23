package whatsapp_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/OBrunoW/catwire/internal/adapters/whatsapp"
)

const (
	migrationPath = "../../../migrations/V001__init.sql"
	libSchema     = "whatsmeow"
	appSchema     = "catwire"
)

func newIntegrationPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	script, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Conn().PgConn().Exec(ctx, string(script)).ReadAll(); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	return pool, databaseURL
}

func openStore(t *testing.T, databaseURL string) *whatsapp.Store {
	t.Helper()
	store, err := whatsapp.OpenStore(context.Background(), databaseURL, waLog.Noop)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func tablesInSchema(t *testing.T, pool *pgxpool.Pool, schema string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE'`,
		schema,
	)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return names
}

func TestOpenStoreCreatesLibrarySchemaAndTables(t *testing.T) {
	pool, databaseURL := newIntegrationPool(t)

	openStore(t, databaseURL)

	var schemaExists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, libSchema,
	).Scan(&schemaExists)
	if err != nil {
		t.Fatalf("check schema: %v", err)
	}
	if !schemaExists {
		t.Fatalf("expected schema %s to exist", libSchema)
	}
	tables := tablesInSchema(t, pool, libSchema)
	if !containsTable(tables, "whatsmeow_device") {
		t.Fatalf("expected whatsmeow_device in %v", tables)
	}
}

func TestOpenStoreTwiceDoesNotFail(t *testing.T) {
	pool, databaseURL := newIntegrationPool(t)
	openStore(t, databaseURL)
	tablesBefore := tablesInSchema(t, pool, libSchema)

	second, err := whatsapp.OpenStore(context.Background(), databaseURL, waLog.Noop)

	if err != nil {
		t.Fatalf("expected second open to succeed, got %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if tablesAfter := tablesInSchema(t, pool, libSchema); len(tablesAfter) != len(tablesBefore) {
		t.Fatalf("expected %d tables after reopen, got %d", len(tablesBefore), len(tablesAfter))
	}
}

func TestOpenStoreDoesNotCollideWithApplicationSchema(t *testing.T) {
	pool, databaseURL := newIntegrationPool(t)
	applicationTablesBefore := tablesInSchema(t, pool, appSchema)

	openStore(t, databaseURL)

	libraryTables := tablesInSchema(t, pool, libSchema)
	applicationTables := tablesInSchema(t, pool, appSchema)
	if len(applicationTables) != len(applicationTablesBefore) {
		t.Fatalf("application schema changed: before %v after %v", applicationTablesBefore, applicationTables)
	}
	for _, table := range applicationTables {
		if strings.HasPrefix(table, "whatsmeow_") {
			t.Fatalf("library table %s leaked into %s", table, appSchema)
		}
	}
	for _, table := range libraryTables {
		if containsTable(applicationTables, table) {
			t.Fatalf("table %s exists in both schemas", table)
		}
	}
}

func containsTable(tables []string, name string) bool {
	for _, table := range tables {
		if table == name {
			return true
		}
	}
	return false
}
