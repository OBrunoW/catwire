package migrator_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/migrator"
	"github.com/OBrunoW/catwire/migrations"
)

func TestApplyIsIdempotentAndSafeUnderConcurrency(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var group sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			failures <- migrator.Apply(ctx, pool, migrations.Files)
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	var applied int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM catwire.schema_migrations WHERE version = 'V001'").Scan(&applied); err != nil {
		t.Fatalf("read versions: %v", err)
	}
	if applied != 1 {
		t.Errorf("expected V001 recorded once, got %d", applied)
	}
}
