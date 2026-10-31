// Package database owns the PostgreSQL connection pool, schema migrations,
// and development seed loading.
package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pool and verifies connectivity.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	// Scan TIMESTAMPTZ as UTC (pgx defaults to the host's local zone), so API
	// responses are always UTC regardless of the server machine's settings.
	cfg.AfterConnect = func(_ context.Context, c *pgx.Conn) error {
		c.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies every not-yet-applied *.sql file from migrationsFS in
// lexical order. Each file runs in its own transaction and is recorded in
// schema_migrations. An advisory lock prevents concurrent migrators.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	const lockID = 72601031 // arbitrary app-specific advisory lock key
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		var applied bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
		).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}

		body, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return err
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
			return err
		}); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		log.Info("applied migration", "version", version)
	}
	return nil
}

// SeedDemoIfEmpty loads the demo dataset when the routes table is empty.
// Callers must only invoke this outside production.
func SeedDemoIfEmpty(ctx context.Context, pool *pgxpool.Pool, seedSQL string, log *slog.Logger) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM routes`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, seedSQL)
		return err
	}); err != nil {
		return fmt.Errorf("load demo seed: %w", err)
	}
	log.Info("loaded demo seed data (SIMULATED)")
	return nil
}
