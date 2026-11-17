// Package dbtest gives each test process a fresh database on an explicitly
// configured test server. It never connects through DATABASE_URL, reuses a
// database, truncates existing data, or falls back to a shared schema.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultAdminDSN = "postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/hyperplane_test_admin?sslmode=disable"
const testDBRefusal = "refusing to run tests against database %q — tests only run in a hyperplane_test_* database; RedirectToTestDB must have been called in this package's TestMain"

// RedirectToTestDB is called once by each package's TestMain. With no
// TEST_DATABASE_URL the suite runs without database tests. Explicit configuration
// is a promise to run them: any setup failure fails the test binary.
func RedirectToTestDB(pkg string) func() {
	configured := os.Getenv("TEST_DATABASE_URL")
	_ = os.Unsetenv("DATABASE_URL")
	if configured == "" {
		return func() {}
	}
	admin, dsn, err := resolveTestDSN(pkg, configured)
	if err != nil {
		setupFailed(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		setupFailed(fmt.Errorf("invalid test connection"))
	}
	name := cfg.Database
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		setupFailed(fmt.Errorf("test server unavailable; start make test-db"))
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		setupFailed(fmt.Errorf("cannot create disposable test database (test role needs CREATEDB)"))
	}
	_ = os.Setenv("DATABASE_URL", dsn)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			setupFailed(fmt.Errorf("cannot reconnect to clean up disposable test database %s", name))
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			setupFailed(fmt.Errorf("cannot remove disposable test database %s", name))
		}
	}
}

func setupFailed(err error) {
	fmt.Fprintf(os.Stderr, "dbtest: %v\n", err)
	os.Exit(1)
}

func resolveTestDSN(pkg, configured string) (string, string, error) {
	if configured == "" {
		configured = defaultAdminDSN
	}
	if err := GuardTestDSN(configured); err != nil {
		return "", "", err
	}
	// URL-only configuration avoids bespoke parsing of quoted libpq DSNs and
	// ambiguous query-parameter overrides. pgx remains the connection parser.
	u, err := url.Parse(configured)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", "", fmt.Errorf("TEST_DATABASE_URL must be a postgres URL")
	}
	if u.RawQuery != "" {
		for key := range u.Query() {
			if key != "sslmode" && key != "connect_timeout" {
				return "", "", fmt.Errorf("unsupported test connection option %q", key)
			}
		}
	}
	var suffix strings.Builder
	for _, r := range strings.ToLower(pkg) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			suffix.WriteRune(r)
		}
	}
	s := suffix.String()
	if s == "" {
		return "", "", fmt.Errorf("empty test package name")
	}
	if len(s) > 24 {
		s = s[:24]
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", err
	}
	u.Path = "/hyperplane_test_" + s + "_" + hex.EncodeToString(nonce[:])
	u.RawPath = ""
	return configured, u.String(), nil
}

// GuardTestDSN is also used immediately before test helpers open connections.
func GuardTestDSN(dsn string) error {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid test database connection")
	}
	if !strings.HasPrefix(config.Database, "hyperplane_test_") {
		return fmt.Errorf(testDBRefusal, config.Database)
	}
	return nil
}
