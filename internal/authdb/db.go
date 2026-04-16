// Package authdb provides the data access layer for the OAuth2 identity system.
// It manages users, session tokens, and schema versioning against a Postgres database.
package authdb

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	dbutil "github.com/stanier/stoa/internal/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RequiredSchemaVersion is the goose migration version this binary expects.
// Bump this constant each time a new migration file is added.
const RequiredSchemaVersion = 4

// DB wraps a pgxpool.Pool and provides typed methods for the auth schema.
type DB struct {
	pool *pgxpool.Pool
}

// Open creates a new DB using the given Postgres connection string.
// The connection is verified with a Ping before returning.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("authdb: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("authdb: ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close releases all pool connections.
func (db *DB) Close() {
	db.pool.Close()
}

// CheckSchemaVersion queries the goose_db_version table and returns an error
// if the current version does not exactly match RequiredSchemaVersion.
// This should be called at agentd startup before the server begins accepting requests.
func CheckSchemaVersion(ctx context.Context, dsn string) error {
	sqlDB, err := dbutil.OpenSQLDB(dsn)
	if err != nil {
		return fmt.Errorf("authdb: schema check: %w", err)
	}
	defer sqlDB.Close()

	current, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("authdb: failed to read schema version: %w", err)
	}

	if current < RequiredSchemaVersion {
		return fmt.Errorf(
			"database schema is at version %d, binary requires %d — run: auth-admin authd migrate up",
			current, RequiredSchemaVersion,
		)
	}
	if current > RequiredSchemaVersion {
		return fmt.Errorf(
			"database schema is at version %d, which is newer than this binary supports (%d) — upgrade agentd",
			current, RequiredSchemaVersion,
		)
	}
	return nil
}

// Migrate runs all pending goose migrations against the given DSN using the
// embedded migration files. Intended to be called via auth-admin (auth-admin authd migrate up).
func Migrate(ctx context.Context, dsn string) error {
	return dbutil.MigrateUp(ctx, dsn, migrationsFS, "migrations")
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, dsn string) error {
	return dbutil.MigrateDown(ctx, dsn, migrationsFS, "migrations")
}

// MigrateStatus prints the current migration status to stdout.
func MigrateStatus(ctx context.Context, dsn string) error {
	return dbutil.MigrateStatus(ctx, dsn, migrationsFS, "migrations")
}
