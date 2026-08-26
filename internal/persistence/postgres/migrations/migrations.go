// Package migrations embeds and applies the customer-service PostgreSQL schema.
package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/tern/v2/migrate"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres/schema"
)

// files is embedded so the migration command behaves the same from source,
// tests, a standalone binary, and a container image.
//
//go:embed *.sql
var files embed.FS

// Up applies every pending migration and returns the resulting schema version.
// Tern records the version separately from the business schema, so migrating
// the business schema down can still update its ledger safely.
func Up(ctx context.Context, connection *pgx.Conn) (int32, error) {
	if ctx == nil {
		return 0, errors.New("postgres migrations: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if connection == nil {
		return 0, errors.New("postgres migrations: connection is nil")
	}

	migrator, err := migrate.NewMigrator(ctx, connection, schema.VersionTable)
	if err != nil {
		return 0, fmt.Errorf("postgres migrations: initialize Tern: %w", err)
	}
	if err := migrator.LoadMigrations(files); err != nil {
		return 0, fmt.Errorf("postgres migrations: load files: %w", err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		return 0, fmt.Errorf("postgres migrations: migrate up: %w", err)
	}
	version, err := migrator.GetCurrentVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres migrations: read current version: %w", err)
	}
	return version, nil
}
