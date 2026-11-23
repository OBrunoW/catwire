package migrator

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationLockKey      int64 = 0x63617477697265
	migrationUnlockWindow       = 5 * time.Second
	migrationPattern            = "V*__*.sql"
)

func Apply(ctx context.Context, pool *pgxpool.Pool, scripts fs.FS) error {
	names, err := fs.Glob(scripts, migrationPattern)
	if err != nil {
		return fmt.Errorf("postgres migrate: list scripts: %w", err)
	}
	sort.Strings(names)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres migrate: acquire connection: %w", err)
	}
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		connection.Release()
		return fmt.Errorf("postgres migrate: acquire lock: %w", err)
	}
	applyErr := applyScripts(ctx, connection, scripts, names)
	return releaseMigrationLock(connection, applyErr)
}

func applyScripts(ctx context.Context, connection *pgxpool.Conn, scripts fs.FS, names []string) error {
	for _, name := range names {
		script, err := fs.ReadFile(scripts, name)
		if err != nil {
			return fmt.Errorf("postgres migrate: read %s: %w", name, err)
		}
		if _, err := connection.Conn().PgConn().Exec(ctx, string(script)).ReadAll(); err != nil {
			return fmt.Errorf("postgres migrate: apply %s: %w", name, err)
		}
		if err := recordMigration(ctx, connection, name); err != nil {
			return err
		}
		slog.Info("migration applied", "version", migrationVersion(name))
	}
	return nil
}

func recordMigration(ctx context.Context, connection *pgxpool.Conn, name string) error {
	statement := "INSERT INTO catwire.schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING"
	if _, err := connection.Exec(ctx, statement, migrationVersion(name)); err != nil {
		return fmt.Errorf("postgres migrate: record %s: %w", name, err)
	}
	return nil
}

func migrationVersion(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".sql")
	version, _, _ := strings.Cut(base, "__")
	return version
}

func releaseMigrationLock(connection *pgxpool.Conn, applyErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), migrationUnlockWindow)
	defer cancel()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
		_ = connection.Hijack().Close(ctx)
		if applyErr != nil {
			return applyErr
		}
		return fmt.Errorf("postgres migrate: release lock: %w", err)
	}
	connection.Release()
	return applyErr
}
