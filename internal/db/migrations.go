// Package db provides shared database migration utilities for goose-managed Postgres schemas.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// MigrateUp runs all pending goose migrations from the given embedded FS.
// The dir argument is the subdirectory within fsys containing the *.sql files (typically "migrations").
func MigrateUp(ctx context.Context, dsn string, fsys fs.FS, dir string) error {
	sqlDB, err := OpenSQLDB(dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: set dialect: %w", err)
	}
	return goose.UpContext(ctx, sqlDB, dir)
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, dsn string, fsys fs.FS, dir string) error {
	sqlDB, err := OpenSQLDB(dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(fsys)
	_ = goose.SetDialect("postgres")
	return goose.DownContext(ctx, sqlDB, dir)
}

// MigrateStatus prints the current migration status to stdout.
func MigrateStatus(ctx context.Context, dsn string, fsys fs.FS, dir string) error {
	sqlDB, err := OpenSQLDB(dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(fsys)
	_ = goose.SetDialect("postgres")
	return goose.StatusContext(ctx, sqlDB, dir)
}

// OpenSQLDB opens a *sql.DB using pgx's stdlib adapter, suitable for use with goose.
func OpenSQLDB(dsn string) (*sql.DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	return stdlib.OpenDB(*cfg.ConnConfig), nil
}
