// Package authdb provides the data access layer for the OAuth2 identity system.
// It manages users, session tokens, and schema versioning against a Postgres database.
package authdb

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	dbutil "github.com/stanier/stoa/internal/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RequiredSchemaVersion is the goose migration version this binary expects.
// Bump this constant each time a new migration file is added.
const RequiredSchemaVersion = 6

// DB wraps a pgxpool.Pool and provides typed methods for the auth schema.
type DB struct {
	pool *pgxpool.Pool
}

// Open creates a new DB using the given Postgres connection string.
// The connection is verified with a Ping before returning.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := dbutil.OpenPool(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("authdb: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close releases all pool connections.
func (db *DB) Close() {
	db.pool.Close()
}

// CheckSchemaVersion verifies the database is at the expected migration version.
// Call this at authd startup before the server begins accepting requests.
func CheckSchemaVersion(ctx context.Context, dsn string) error {
	return dbutil.CheckSchemaVersion(ctx, dsn, RequiredSchemaVersion)
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
