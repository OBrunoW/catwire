package whatsapp

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const (
	storeSchema  = "whatsmeow"
	storeDialect = "pgx"
)

type Store struct {
	container *sqlstore.Container
	db        *sql.DB
}

func OpenStore(ctx context.Context, databaseURL string, logger waLog.Logger) (*Store, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("whatsapp store: parse database url: %w", err)
	}
	config.RuntimeParams["search_path"] = storeSchema
	db := stdlib.OpenDB(*config)
	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	container := sqlstore.NewWithDB(db, storeDialect, logger)
	if err := container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("whatsapp store: upgrade: %w", err)
	}
	return &Store{container: container, db: db}, nil
}

func createSchema(ctx context.Context, db *sql.DB) error {
	statement := "CREATE SCHEMA IF NOT EXISTS " + pgx.Identifier{storeSchema}.Sanitize()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("whatsapp store: create schema: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.container.Close()
}
