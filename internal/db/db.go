// Package db opens the Postgres connection pool and applies the schema.
package db

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations.sql
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

// Migrate applies the embedded schema. It is idempotent and safe to run on
// every boot.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sql, err := migrationsFS.ReadFile("migrations.sql")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
