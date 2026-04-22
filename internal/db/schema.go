package db

import (
	"context"
	"fmt"

	"github.com/pressly/goose/v3"
)

// CheckSchemaVersion verifies the goose-managed database is at exactly the required version.
// Call this at service startup before accepting requests to catch migration drift early.
func CheckSchemaVersion(ctx context.Context, dsn string, required int64) error {
	sqlDB, err := OpenSQLDB(dsn)
	if err != nil {
		return fmt.Errorf("db: schema check: %w", err)
	}
	defer sqlDB.Close()

	current, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("db: read schema version: %w", err)
	}

	if current < required {
		return fmt.Errorf(
			"database schema is at version %d, binary requires %d — run migrations",
			current, required,
		)
	}
	if current > required {
		return fmt.Errorf(
			"database schema is at version %d, which is newer than this binary supports (%d) — upgrade binary",
			current, required,
		)
	}
	return nil
}
