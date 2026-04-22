package localidp

import (
	"context"
	"embed"
	"fmt"

	dbutil "github.com/stanier/stoa/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// db wraps a pgxpool.Pool with typed methods for the local-idp schema.
type db struct {
	pool *pgxpool.Pool
}

func openDB(ctx context.Context, dsn string) (*db, error) {
	pool, err := dbutil.OpenPool(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("localidp: %w", err)
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
