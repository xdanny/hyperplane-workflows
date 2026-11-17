package persona

// import.go — registering the charters.
//
// `make seed` imports personas/*.md the same way it imports skills, context
// bundles and trusted processes: CONTENT is refreshed, identity is not. A
// charter amended in git lands on the next seed with a new version hash; a
// charter nobody touched is rewritten to the same values, so re-seeding is a
// no-op you can run as often as you like.
//
// The repo stays the source of truth. There is no write API for a charter, and
// this is the only writer.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// readmeName is the directory's own documentation, not a reviewer.
const readmeName = "README.md"

// Import registers every charter in dir. A missing directory is not an error —
// a clone without personas/ simply has no registered reviewers.
//
// It returns the ids it registered so a caller can report them; the first
// unparseable charter fails the whole import rather than half-registering a
// roster, because a partially-imported set of reviewers is the kind of thing
// nobody notices until a review is missing.
func Import(ctx context.Context, st *store.Store, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("personas: read %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == readmeName {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ids, fmt.Errorf("personas: read %s: %w", name, err)
		}
		c, err := Parse(name, string(b))
		if err != nil {
			return ids, err
		}
		row, err := c.ToStore()
		if err != nil {
			return ids, err
		}
		if err := st.UpsertPersona(ctx, row); err != nil {
			return ids, err
		}
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids, nil
}
