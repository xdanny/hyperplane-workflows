package dbtest

import (
	"strings"
	"testing"
)

func TestGuardRefusesRealDatabase(t *testing.T) {
	err := GuardTestDSN("postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/hyperplane")
	if err == nil {
		t.Fatal("expected real database DSN to be refused")
	}
	want := `refusing to run tests against database "hyperplane" — tests only run in a hyperplane_test_* database; RedirectToTestDB must have been called in this package's TestMain`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestGuardAcceptsTestDatabase(t *testing.T) {
	if err := GuardTestDSN("postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/hyperplane_test_store"); err != nil {
		t.Fatalf("expected test database DSN to be accepted: %v", err)
	}
}

func TestResolveUnsetDatabaseURL(t *testing.T) {
	adminDSN, testDSN, err := resolveTestDSN("HTTP API", "")
	if err != nil {
		t.Fatalf("resolve unset DATABASE_URL: %v", err)
	}
	if adminDSN != defaultAdminDSN {
		t.Fatalf("admin DSN = %q, want %q", adminDSN, defaultAdminDSN)
	}
	if !strings.HasPrefix(testDSN, "postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/") {
		t.Fatalf("test DSN is not on default host: %q", testDSN)
	}
	if !strings.Contains(testDSN, "/hyperplane_test_httpapi") {
		t.Fatalf("test DSN = %q, want hyperplane_test_httpapi", testDSN)
	}
}

func TestResolveRefusesProductionAdmin(t *testing.T) {
	if _, _, err := resolveTestDSN("engine", "postgres://user:password@127.0.0.1/prod"); err == nil {
		t.Fatal("test setup accepted production admin database")
	}
}

func TestResolveUsesUniqueDatabasePerInvocation(t *testing.T) {
	_, a, err := resolveTestDSN("engine", "postgres://user:password@127.0.0.1/hyperplane_test_admin")
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := resolveTestDSN("engine", "postgres://user:password@127.0.0.1/hyperplane_test_admin")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("concurrent test invocations reuse the same database")
	}
}
