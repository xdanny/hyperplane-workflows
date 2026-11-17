package schemaprovenance_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
	"github.com/xdanny/hyperplane-workflows/internal/store/migrations"
	"github.com/xdanny/hyperplane-workflows/internal/store/schemaprovenance"
)

// testServerDSN returns the DATABASE_URL of a Postgres server this test
// process may freely CREATE/DROP throwaway databases on. Per repo policy this
// must be an ephemeral, hyperplane-owned container — never the clone-and-run
// default at 127.0.0.1:5432, which on some developer machines is an SSH
// tunnel to a foreign database. Tests skip (not fail) when unset, matching
// the rest of the suite's behavior on machines with no test DB configured.
func testServerDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping schemaprovenance DB tests")
	}
	if err := dbtest.GuardTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	return dsn
}

// withTempDB creates a throwaway database on the same server as serverDSN,
// hands fn a connection to it, and drops the database on cleanup. Each test
// gets its own database so seeded schema_migrations rows / tables never leak
// between cases.
func withTempDB(t *testing.T, serverDSN string, fn func(ctx context.Context, conn *pgx.Conn, dsn string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, serverDSN)
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	defer admin.Close(ctx)

	dbName := fmt.Sprintf("hyperplane_test_schemaprovenance_case_%d_%d", time.Now().UnixNano(), rand.Intn(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		dropConn, err := pgx.Connect(dropCtx, serverDSN)
		if err != nil {
			return
		}
		defer dropConn.Close(dropCtx)
		_, _ = dropConn.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	})

	tempDSN, err := swapDBNameForTest(serverDSN, dbName)
	if err != nil {
		t.Fatalf("build temp dsn: %v", err)
	}
	if err := dbtest.GuardTestDSN(tempDSN); err != nil {
		t.Fatal(err)
	}

	conn, err := pgx.Connect(ctx, tempDSN)
	if err != nil {
		t.Fatalf("connect to temp db: %v", err)
	}
	defer conn.Close(ctx)

	fn(ctx, conn, tempDSN)
}

// swapDBNameForTest is a minimal, test-local DSN-database-name swap (the
// production version lives in internal/dbtest, which this leaf package must
// not import).
func swapDBNameForTest(dsn, newDB string) (string, error) {
	if !strings.Contains(dsn, "://") {
		return "", fmt.Errorf("expected a URL-style DSN, got %q", dsn)
	}
	idx := strings.LastIndex(dsn, "/")
	if idx < 0 {
		return "", fmt.Errorf("no path in DSN %q", dsn)
	}
	base := dsn[:idx]
	rest := dsn[idx+1:]
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		return base + "/" + newDB + rest[q:], nil
	}
	return base + "/" + newDB, nil
}

func TestCheckAppliedMigrations_NoTable_OK(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		if err := schemaprovenance.CheckAppliedMigrations(ctx, conn, dsn); err != nil {
			t.Fatalf("expected nil error for a db with no schema_migrations table, got: %v", err)
		}
	})
}

func TestCheckAppliedMigrations_KnownSubset_OK(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		mustExec(t, ctx, conn, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`)
		known := migrations.Names()
		if len(known) == 0 {
			t.Fatal("expected at least one embedded migration name")
		}
		mustExec(t, ctx, conn, `INSERT INTO schema_migrations(version) VALUES ($1)`, known[0])

		if err := schemaprovenance.CheckAppliedMigrations(ctx, conn, dsn); err != nil {
			t.Fatalf("expected nil error for a known migration subset, got: %v", err)
		}
	})
}

func TestCheckAppliedMigrations_UnknownEntry_ErrorsAndNamesIt(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		mustExec(t, ctx, conn, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`)
		known := migrations.Names()
		if len(known) == 0 {
			t.Fatal("expected at least one embedded migration name")
		}
		mustExec(t, ctx, conn, `INSERT INTO schema_migrations(version) VALUES ($1)`, known[0])
		mustExec(t, ctx, conn, `INSERT INTO schema_migrations(version) VALUES ($1)`, "0027_foreign_branch.sql")

		err := schemaprovenance.CheckAppliedMigrations(ctx, conn, dsn)
		if err == nil {
			t.Fatal("expected an error for an unrecognized migration entry, got nil")
		}
		if !strings.Contains(err.Error(), "0027_foreign_branch.sql") {
			t.Errorf("expected error to name the unknown migration, got: %v", err)
		}
		if !strings.Contains(err.Error(), "1 migration(s) this build does not know") {
			t.Errorf("expected error to report the unknown count, got: %v", err)
		}
		if !strings.Contains(err.Error(), "refusing to run tests or migrate against a foreign schema") {
			t.Errorf("expected error to explain the refusal, got: %v", err)
		}
		if !strings.Contains(err.Error(), "postgres:16-alpine") {
			t.Errorf("expected error to suggest a throwaway container, got: %v", err)
		}
	})
}

func TestCheckFreshDB_Empty_OK(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		if err := schemaprovenance.CheckFreshDB(ctx, conn, "public", dsn); err != nil {
			t.Fatalf("expected nil error for a brand-new empty db, got: %v", err)
		}
	})
}

func TestCheckFreshDB_ForeignTablesNoMigrationsTable_Errors(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		mustExec(t, ctx, conn, `CREATE TABLE some_foreign_table (id INT)`)

		err := schemaprovenance.CheckFreshDB(ctx, conn, "public", dsn)
		if err == nil {
			t.Fatal("expected an error for a db with unexplained pre-existing tables, got nil")
		}
		if !strings.Contains(err.Error(), "some_foreign_table") {
			t.Errorf("expected error to name the foreign table, got: %v", err)
		}
		if !strings.Contains(err.Error(), "refusing to run tests or migrate against a foreign schema") {
			t.Errorf("expected error to explain the refusal, got: %v", err)
		}
	})
}

func TestCheckFreshDB_ForeignMigrationEntry_Errors(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		mustExec(t, ctx, conn, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`)
		mustExec(t, ctx, conn, `INSERT INTO schema_migrations(version) VALUES ($1)`, "0099_divergent_branch.sql")

		err := schemaprovenance.CheckFreshDB(ctx, conn, "public", dsn)
		if err == nil {
			t.Fatal("expected an error for a foreign schema_migrations entry, got nil")
		}
		if !strings.Contains(err.Error(), "0099_divergent_branch.sql") {
			t.Errorf("expected error to name the unknown migration, got: %v", err)
		}
	})
}

func TestCheckFreshDB_ReusedOwnDB_OK(t *testing.T) {
	serverDSN := testServerDSN(t)
	withTempDB(t, serverDSN, func(ctx context.Context, conn *pgx.Conn, dsn string) {
		mustExec(t, ctx, conn, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`)
		known := migrations.Names()
		if len(known) == 0 {
			t.Fatal("expected at least one embedded migration name")
		}
		mustExec(t, ctx, conn, `INSERT INTO schema_migrations(version) VALUES ($1)`, known[0])
		mustExec(t, ctx, conn, `CREATE TABLE runs (run_id TEXT PRIMARY KEY)`)

		if err := schemaprovenance.CheckFreshDB(ctx, conn, "public", dsn); err != nil {
			t.Fatalf("expected nil error for a db this build previously migrated, got: %v", err)
		}
	})
}

func TestLabel(t *testing.T) {
	cases := map[string]string{
		"postgres://hyperplane:hyperplane@127.0.0.1:5432/hyperplane?sslmode=disable": "127.0.0.1:5432/hyperplane",
		"postgres://u:p@db.internal:6543/mydb":                                       "db.internal:6543/mydb",
		"host=127.0.0.1 port=5432 dbname=hyperplane user=x password=y":               "127.0.0.1:5432/hyperplane",
	}
	for dsn, want := range cases {
		if got := schemaprovenance.Label(dsn); got != want {
			t.Errorf("Label(%q) = %q, want %q", dsn, got, want)
		}
	}
}

func mustExec(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
