package localidp

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	dbutil "github.com/stanier/stoa/internal/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// db wraps a pgxpool.Pool with typed methods for the local-idp schema.
type db struct {
	pool *pgxpool.Pool
}

func openDB(ctx context.Context, dsn string) (*db, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("localidp: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("localidp: ping: %w", err)
	}
	return &db{pool: pool}, nil
}

func (d *db) close() { d.pool.Close() }

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
