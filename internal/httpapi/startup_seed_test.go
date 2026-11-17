package httpapi

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A deployed instance with no templates is an instance nobody can start work
// from. Startup fills an EMPTY install and leaves every other one alone.
//
// No test in this package runs in parallel, so emptying the disposable test
// database's templates is safe here and is the only way to observe the
// first-start path.
func TestSeedDefaultsOnStartup(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL")) // TestMain supplies the disposable database.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM workflow_templates`); err != nil {
		t.Fatal(err)
	}

	line := SeedDefaultsOnStartup(ctx, st)
	if !strings.Contains(line, "seeded") || strings.Contains(line, "not seeded") {
		t.Fatalf("an empty install was not seeded: %q", line)
	}
	templates, err := st.ListTemplates(ctx)
	if err != nil || len(templates) == 0 {
		t.Fatalf("no templates after seeding: %v %d", err, len(templates))
	}

	// A second start does not re-seed: an operator's deletions are their own.
	again := SeedDefaultsOnStartup(ctx, st)
	if !strings.Contains(again, "already present") {
		t.Fatalf("startup re-seeded a populated install: %q", again)
	}

	t.Setenv(SeedDefaultsEnv, "off")
	if off := SeedDefaultsOnStartup(ctx, st); !strings.Contains(off, "not seeded") {
		t.Fatalf("the opt-out was ignored: %q", off)
	}
}
