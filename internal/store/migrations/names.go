// Package migrations embeds the SQL migration files applied by
// internal/store's runner and exposes just their filenames. It is a
// dependency-free leaf package (embed/sort/strings only) so that both
// internal/store and internal/dbtest can import it to learn which migration
// names this build knows about, without creating an import cycle between
// them (internal/store's own tests import internal/dbtest, so internal/dbtest
// must never import internal/store — see internal/dbtest's package doc).
package migrations

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.sql
var fs embed.FS

// Names returns the sorted list of migration filenames embedded in this
// build, e.g. "0001_init.sql", "0002_evals.sql", ....
func Names() []string {
	entries, err := fs.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Known returns Names() as a set for O(1) membership checks.
func Known() map[string]bool {
	known := make(map[string]bool, len(Names()))
	for _, n := range Names() {
		known[n] = true
	}
	return known
}
