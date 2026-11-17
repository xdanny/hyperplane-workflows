package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// SeedEvalsets reads *.eval.json files from dir and upserts them into the store.
func SeedEvalsets(ctx context.Context, st *store.Store, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var count int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".eval.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return count, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		var es store.Evalset
		if err := json.Unmarshal(data, &es); err != nil {
			return count, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		if err := st.UpsertEvalset(ctx, es); err != nil {
			return count, fmt.Errorf("upsert %s: %w", es.ID, err)
		}
		count++
	}
	return count, nil
}
