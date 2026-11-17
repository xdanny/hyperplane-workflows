// Package schemaprovenance guards against running Hyperplane's tests or
// migrations against a database that carries schema from a different,
// divergent build — e.g. a developer's DATABASE_URL accidentally pointing at
// an SSH tunnel to a foreign database. Applying our migrations to such a
// database, or truncating/mutating its tables in test setup, is meaningless
// at best and destructive at worst.
//
// It is a dependency-free leaf package (stdlib + pgx only) so that both
// internal/store and internal/dbtest can import it without creating an
// import cycle: internal/store's own package tests import internal/dbtest
// (via TestMain), so internal/dbtest must never import internal/store.
package schemaprovenance

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/xdanny/hyperplane-workflows/internal/store/migrations"
)

// Querier is satisfied by both *pgxpool.Pool and *pgx.Conn.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CheckAppliedMigrations reads the schema_migrations table (the applied-
// migrations table internal/store's runner maintains) and compares its
// entries against the migration filenames embedded in this build. A missing
// table, or a table whose entries are all known to this build, is fine — the
// database is either untouched or ours. Any applied entry this build does
// not recognize means the database belongs to a different, divergent build;
// refuse rather than migrate or test against it.
func CheckAppliedMigrations(ctx context.Context, q Querier, dsn string) error {
	exists, err := hasTable(ctx, q, "schema_migrations")
	if err != nil {
		return fmt.Errorf("schema provenance check: %w", err)
	}
	if !exists {
		return nil
	}

	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("schema provenance check: read schema_migrations: %w", err)
	}
	defer rows.Close()

	known := migrations.Known()
	var unknown []string
	for rows.Next() {
		var version string
		if scanErr := rows.Scan(&version); scanErr != nil {
			return fmt.Errorf("schema provenance check: scan version: %w", scanErr)
		}
		if !known[version] {
			unknown = append(unknown, version)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("schema provenance check: %w", err)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return foreignSchemaErr(dsn, "carries %d migration(s) this build does not know (first: %s)", len(unknown), unknown[0])
}

// CheckFreshDB is for internal/dbtest's use: it guards a database it just
// created (or reused) for a test package, before that package's own
// store.Migrate call and before any destructive setup (e.g. truncation of
// existing tables) runs against it.
//
// A brand-new database has zero tables. internal/dbtest does legitimately
// reuse a database across `go test` runs of the same package, in which case
// the table set is exactly what our own migrations created and
// schema_migrations names a subset of this build's known migrations. But if
// the database already has tables and no schema_migrations table (no
// legitimate path creates any other table first), or schema_migrations
// contains entries this build doesn't know, the database's schema came from
// somewhere else — most likely a `CREATE DATABASE ... TEMPLATE template1`
// where template1 itself was poisoned with a foreign schema. Refuse rather
// than truncate and migrate a database we don't recognize.
func CheckFreshDB(ctx context.Context, q Querier, schema, dsn string) error {
	tables, err := listTables(ctx, q, schema)
	if err != nil {
		return fmt.Errorf("schema provenance check: %w", err)
	}
	if len(tables) == 0 {
		return nil
	}
	hasMigrationsTable := false
	for _, t := range tables {
		if t == "schema_migrations" {
			hasMigrationsTable = true
			break
		}
	}
	if !hasMigrationsTable {
		sort.Strings(tables)
		return foreignSchemaErr(dsn, "already contains %d table(s) this build did not create (first: %s)", len(tables), tables[0])
	}
	return CheckAppliedMigrations(ctx, q, dsn)
}

func foreignSchemaErr(dsn, lead string, args ...any) error {
	return fmt.Errorf(
		"database at %s "+lead+" — refusing to run tests or migrate against a foreign schema; "+
			"point DATABASE_URL at an empty or hyperplane-owned database (e.g. a throwaway postgres:16-alpine container)",
		append([]any{Label(dsn)}, args...)...,
	)
}

func hasTable(ctx context.Context, q Querier, table string) (bool, error) {
	rows, err := q.Query(ctx, `SELECT to_regclass($1) IS NOT NULL`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var exists bool
	if rows.Next() {
		if scanErr := rows.Scan(&exists); scanErr != nil {
			return false, scanErr
		}
	}
	return exists, rows.Err()
}

func listTables(ctx context.Context, q Querier, schema string) ([]string, error) {
	if schema == "" {
		schema = "public"
	}
	rows, err := q.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = $1`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if scanErr := rows.Scan(&t); scanErr != nil {
			return nil, scanErr
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// Label renders a DSN as a short, credential-free "host:port/db" string for
// error messages and log lines.
func Label(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			db := strings.TrimPrefix(u.Path, "/")
			host := u.Host
			if host == "" {
				host = "127.0.0.1:5432"
			}
			if db == "" {
				return host
			}
			return host + "/" + db
		}
	}
	// Keyword/space-separated DSN (host=... port=... dbname=...).
	var host, port, db string
	for _, f := range strings.Fields(dsn) {
		switch {
		case strings.HasPrefix(f, "host="):
			host = strings.TrimPrefix(f, "host=")
		case strings.HasPrefix(f, "port="):
			port = strings.TrimPrefix(f, "port=")
		case strings.HasPrefix(f, "dbname="):
			db = strings.TrimPrefix(f, "dbname=")
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "5432"
	}
	label := host + ":" + port
	if db != "" {
		label += "/" + db
	}
	return label
}
