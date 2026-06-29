package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsFS embeds the .sql files so the binary is fully self-contained —
// no migration files need to ship alongside it.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending migrations in filename order. Each migration runs
// inside its own transaction and is recorded in schema_migrations so it is
// applied at most once. Safe to run on every startup (idempotent).
//
// This is a deliberately small migrator — for complex needs swap in
// golang-migrate or goose, which read the same migrations/ directory.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // filename order == apply order

	applied := 0
	for _, name := range names {
		ok, err := migrationApplied(ctx, pool, name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		if err := applyMigration(ctx, pool, name, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		log.Info("migration applied", "version", name)
		applied++
	}

	if applied == 0 {
		log.Info("database schema up to date")
	}
	return nil
}

func migrationApplied(ctx context.Context, pool *pgxpool.Pool, version string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	return exists, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version, sql string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
		return err
	})
}
