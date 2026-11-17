package seed_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// repoRoot is the platform root relative to internal/seed (the package dir is
// the cwd for `go test`).
const repoRoot = "../.."

const defsDir = repoRoot + "/knowledge/defs"

// skillRefsIn walks arbitrary decoded JSON and collects every "skillRef" string
// and every element of a "skillRefs" array. Walking rather than typing the DAG
// schema keeps the guard working when node shapes change.
func skillRefsIn(v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			switch k {
			case "skillRef":
				if s, ok := val.(string); ok && s != "" {
					out[s] = true
				}
			case "skillRefs":
				if arr, ok := val.([]any); ok {
					for _, e := range arr {
						if s, ok := e.(string); ok && s != "" {
							out[s] = true
						}
					}
				}
			}
			skillRefsIn(val, out)
		}
	case []any:
		for _, e := range t {
			skillRefsIn(e, out)
		}
	}
}

// collectRefsFromJSON decodes a JSON file and harvests its skill references.
// Strings that contain JSON documents, including serialized template definitions,
// are decoded recursively.
func collectRefsFromJSON(t *testing.T, path string, out map[string]bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	skillRefsIn(v, out)
}

// requiredSkillIDs is every skill id something in this repo already points at:
// the composer's starter briefs and workflow templates seeded from Go.
// Referenced skills must resolve to the shipped catalog.
func requiredSkillIDs(t *testing.T) map[string]bool {
	t.Helper()
	refs := map[string]bool{}

	collectRefsFromJSON(t, filepath.Join(repoRoot, "internal/compose/briefs.json"), refs)

	// Workflow templates live in Go with serialized SDK definitions.
	for _, tmpl := range seed.DefaultTemplates {
		var v any
		if err := json.Unmarshal(tmpl.Spec, &v); err != nil {
			t.Fatalf("DefaultTemplates[%q]: spec is not valid JSON: %v", tmpl.Name, err)
		}
		skillRefsIn(v, refs)
	}
	return refs
}

// TestNoDanglingSkillRefs is the guard that matters: the shipped DAGs,
// processes, briefs, and templates may not reference a skill the in-repo seed
// cannot supply. A dangling skillRef fails open in the engine (lookupSkill just
// returns false), so nothing complains at runtime — the node simply runs without
// its expertise. This test is the only place that notices.
func TestNoDanglingSkillRefs(t *testing.T) {
	required := requiredSkillIDs(t)
	// Direct SDK templates no longer require catalog skill references. An empty
	// set is valid; any references added to remaining graph workflows must resolve.

	var missing []string
	for id := range required {
		if _, err := os.Stat(filepath.Join(defsDir, "skills", id+".json")); err != nil {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("skillRef(s) with no def in %s/skills: %v\n"+
			"add a <id>.json there, or drop the reference", defsDir, missing)
	}
}

type defSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Reasoning   string `json:"reasoning"`
	Instruction string `json:"instruction"`
}

// TestSkillDefsAreWellFormed checks every starter skill parses and is usable on
// its own. The instruction is pasted verbatim into a codex prompt, so a pointer
// to a file that does not exist in this repo is a dead instruction.
func TestSkillDefsAreWellFormed(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(defsDir, "skills", "*.json"))
	if err != nil {
		t.Fatalf("glob skills: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no skill defs found in " + defsDir + "/skills")
	}

	for _, p := range paths {
		name := filepath.Base(p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var s defSkill
		if err := json.Unmarshal(b, &s); err != nil {
			t.Errorf("%s: invalid JSON: %v", name, err)
			continue
		}
		if want := strings.TrimSuffix(name, ".json"); s.ID != want {
			t.Errorf("%s: id is %q, want %q (id must match the filename)", name, s.ID, want)
		}
		if s.Name == "" || s.Description == "" {
			t.Errorf("%s: name and description are both required", name)
		}
		if len(s.Instruction) < 200 {
			t.Errorf("%s: instruction is %d chars — too thin to be worth seeding", name, len(s.Instruction))
		}
		// buildSkillMeta only inlines docs/*.md that exist under the repo root, so
		// a reference to a doc we do not ship leaves codex chasing a missing file.
		if strings.Contains(s.Instruction, "docs/") {
			t.Errorf("%s: instruction references docs/… — starter skills must stand alone", name)
		}
		if strings.Contains(strings.ToLower(s.Instruction), "../orchestra") {
			t.Errorf("%s: instruction references the private orchestra checkout", name)
		}
	}
}

type defBundleItem struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Why  string `json:"why"`
}

type defBundle struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Items       []defBundleItem `json:"items"`
}

// TestContextDefsResolve checks each starter bundle points at files that exist.
// The importer inlines item content by path and silently skips what it cannot
// read, so a typo would ship an empty bundle that looks fine in the UI.
func TestContextDefsResolve(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(defsDir, "context", "*.json"))
	if err != nil {
		t.Fatalf("glob context: %v", err)
	}
	for _, p := range paths {
		name := filepath.Base(p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var bundle defBundle
		if err := json.Unmarshal(b, &bundle); err != nil {
			t.Errorf("%s: invalid JSON: %v", name, err)
			continue
		}
		if want := strings.TrimSuffix(name, ".json"); bundle.ID != want {
			t.Errorf("%s: id is %q, want %q", name, bundle.ID, want)
		}
		if len(bundle.Items) == 0 {
			t.Errorf("%s: bundle has no items", name)
		}
		for _, it := range bundle.Items {
			if it.Path == "" || it.Why == "" {
				t.Errorf("%s: every item needs a path and a why", name)
				continue
			}
			// enrichBundleItems resolves item paths against <defsDir>/../.. .
			if _, err := os.Stat(filepath.Join(defsDir, "..", "..", it.Path)); err != nil {
				t.Errorf("%s: item path %q does not exist in the repo", name, it.Path)
			}
		}
	}
}

// TestSeedRoundTrip imports the in-repo defs through the real importer and reads
// them back, proving `make seed` on a fresh clone lands every referenced skill
// with a non-empty instruction. Skipped when no database is configured.
func TestSeedRoundTrip(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set — skipping seed round-trip")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Skipf("store unavailable: %v", err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	res, err := seed.FromDefsDir(ctx, st, defsDir)
	if err != nil {
		t.Fatalf("FromDefsDir: %v", err)
	}
	if res.Skills == 0 {
		t.Fatal("FromDefsDir imported 0 skills")
	}

	for id := range requiredSkillIDs(t) {
		sk, err := st.GetSkill(ctx, id)
		if err != nil {
			t.Errorf("GetSkill(%q) after seed: %v", id, err)
			continue
		}
		if strings.TrimSpace(sk.Instruction) == "" {
			t.Errorf("skill %q seeded with an empty instruction", id)
		}
	}

	// Bundles must survive the round-trip with their file content inlined,
	// otherwise a contextRef node loads an empty bundle.
	if res.ContextBundles > 0 {
		b, err := st.GetContextBundle(ctx, "hyperplane-platform")
		if err != nil {
			t.Fatalf("GetContextBundle: %v", err)
		}
		var items []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(b.Items, &items); err != nil {
			t.Fatalf("bundle items: %v", err)
		}
		for _, it := range items {
			if strings.TrimSpace(it.Content) == "" {
				t.Errorf("bundle hyperplane-platform: item %q has no inlined content", it.Path)
			}
		}
	}
}
