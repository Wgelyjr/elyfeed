// Package db opens the Postgres connection pool and applies the schema.
package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open builds a connection pool from a DSN and verifies connectivity.
//
// Postgres may not be accepting connections yet (e.g. during containerized
// startup), so the initial ping is retried on a short backoff for a bounded
// period before failing.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	const (
		maxWait   = 60 * time.Second
		pingTTL   = 5 * time.Second
		retryWait = 2 * time.Second
	)
	deadline := time.Now().Add(maxWait)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, pingTTL)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("connect to database (giving up after %s): %w", maxWait, err)
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(retryWait):
		}
	}
}

// Migrate applies any unapplied versioned migrations from the embedded
// migrations/ directory, in filename order, each within its own transaction.
//
// Migrations are recorded in a schema_migrations table and are never
// re-applied, so it is safe to run on every boot.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := pool.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := parseVersion(name)
		if err != nil {
			return fmt.Errorf("parse migration %s: %w", name, err)
		}
		applied, err := migrationApplied(ctx, pool, version)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("run migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, pool *pgxpool.Pool, version int) (bool, error) {
	var ok bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
	).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// parseVersion extracts the leading integer from a migration filename such as
// "0002_multiuser.sql" (-> 2).
func parseVersion(name string) (int, error) {
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("no version prefix")
	}
	return strconv.Atoi(name[:i])
}
