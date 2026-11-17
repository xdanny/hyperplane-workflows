package httpapi

import (
	"context"
	"fmt"
	"os"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A deployed instance shipped with zero templates, because `make seed` is a
// developer command and nothing in the launcher ran it. A first start with an
// empty templates table therefore installs the shipped defaults, and says so.
//
// It runs only when the table is EMPTY: an operator who deleted a template did
// so on purpose, and startup must not put it back. `make seed` remains the way
// to refresh existing templates.

// SeedDefaultsEnv turns startup seeding off when set to "off".
const SeedDefaultsEnv = "HYPERPLANE_SEED_DEFAULTS"

// SeedDefaultsOnStartup installs the shipped defaults when no template exists.
// It returns the single line the caller logs, so a reader of the log always
// learns what was decided — including that nothing was.
func SeedDefaultsOnStartup(ctx context.Context, st *store.Store) string {
	if os.Getenv(SeedDefaultsEnv) == "off" {
		return "default templates: not seeded (" + SeedDefaultsEnv + "=off)"
	}
	templates, err := st.ListTemplates(ctx)
	if err != nil {
		return fmt.Sprintf("default templates: not seeded (could not read templates: %v)", err)
	}
	if len(templates) > 0 {
		return fmt.Sprintf("default templates: %d already present; run `make seed` to refresh them", len(templates))
	}
	if err := seed.Defaults(ctx, st); err != nil {
		return fmt.Sprintf("default templates: seeding failed: %v", err)
	}
	seeded, err := st.ListTemplates(ctx)
	if err != nil {
		return "default templates: seeded"
	}
	return fmt.Sprintf("default templates: seeded %d into an empty install", len(seeded))
}
