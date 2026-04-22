// Package agentdb provides Postgres persistence for agentd sessions and messages.
// It is optional — agentd falls back to filesystem persistence when no DB is configured.
package agentdb

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
const RequiredSchemaVersion = 10

// DB wraps a pgxpool.Pool with typed methods for the agentd schema.
type DB struct {
	pool *pgxpool.Pool
}

// Open creates a new DB from the given Postgres DSN.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := dbutil.OpenPool(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("agentdb: %w", err)
	}
	return &DB{pool: pool}, nil
}

// CheckSchemaVersion verifies the database is at the expected migration version.
// Call this at agentd startup before the server begins accepting requests.
func CheckSchemaVersion(ctx context.Context, dsn string) error {
	return dbutil.CheckSchemaVersion(ctx, dsn, RequiredSchemaVersion)
}

// Close releases pool connections.
func (d *DB) Close() { d.pool.Close() }

// Migrate runs all pending goose migrations.
func Migrate(ctx context.Context, dsn string) error {
	return dbutil.MigrateUp(ctx, dsn, migrationsFS, "migrations")
}

// MigrateDown rolls back one migration.
func MigrateDown(ctx context.Context, dsn string) error {
	return dbutil.MigrateDown(ctx, dsn, migrationsFS, "migrations")
}

// MigrateStatus prints current migration status.
func MigrateStatus(ctx context.Context, dsn string) error {
	return dbutil.MigrateStatus(ctx, dsn, migrationsFS, "migrations")
}
