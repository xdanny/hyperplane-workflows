// Package store is the Postgres-backed source of truth for Hyperplane:
// runs, nodes, events, records, and the knowledge tables (skills, context
// bundles, reviewers, connectors). It exposes a thin pgx pool plus a
// minimal embedded-SQL migration runner so `docker compose up` is self-setting.
package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xdanny/hyperplane-workflows/internal/store/schemaprovenance"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a pgx connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects to Postgres with a bounded retry so the API can start slightly
// ahead of the database container during `docker compose up`.
func Open(ctx context.Context, dsn string) (*Store, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return &Store{Pool: pool}, nil
			}
			pool.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("connect postgres: %w", lastErr)
}

// openOnce connects with a single attempt and a short ping timeout. Used by
// tests so they skip fast when no database is reachable.
func openOnce(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}

// CheckSchemaProvenance reads the schema_migrations table (the applied-
// migrations table Migrate maintains) and refuses to proceed if it carries
// migration names this build's embedded migration set does not recognize —
// i.e. dsn points at a database whose schema came from a different,
// divergent build. Call this BEFORE Migrate. A missing schema_migrations
// table, or one whose entries are all known to this build, returns nil.
func CheckSchemaProvenance(ctx context.Context, pool *pgxpool.Pool, dsn string) error {
	return schemaprovenance.CheckAppliedMigrations(ctx, pool, dsn)
}

// Migrate serializes startup and applies the pending schema atomically. A failed
// migration rolls back the whole batch; no caller sees a partially upgraded DB.
func (s *Store) Migrate(ctx context.Context) (applied []string, err error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// Transaction-scoped lock releases on commit, rollback, or lost connection.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(684795134)`); err != nil {
		return nil, err
	}
	if err := schemaprovenance.CheckAppliedMigrations(ctx, tx, ""); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
 )`); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			return nil, fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, name); err != nil {
			return nil, fmt.Errorf("record %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return applied, nil
}
