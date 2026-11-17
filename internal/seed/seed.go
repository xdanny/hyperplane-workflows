// Package seed imports optional skills and context bundles from local JSON
// catalogs into the Hyperplane store.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Result counts what was imported.
type Result struct {
	Skills         int
	ContextBundles int
}

// scrubNUL removes NUL bytes from inlined doc content. Postgres jsonb rejects the
//
//	escape ("unsupported Unicode escape sequence"), so a doc with a stray NUL
//
// would otherwise break the whole seed.
func scrubNUL(s string) string {
	if !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

var docRefRe = regexp.MustCompile(`docs/([\w./-]+\.md)`)

// buildSkillMeta loads the markdown docs a skill's instruction references (e.g.
// "docs/web-art-direction.md") from <defsDir>/../../docs and stores the combined
// text under metadata.doc, so the FULL skill can be loaded into a codex session.
func buildSkillMeta(defsDir, instruction string, existing json.RawMessage) json.RawMessage {
	docsDir := filepath.Join(defsDir, "..", "..", "docs")
	seen := map[string]bool{}
	var combined strings.Builder
	for _, m := range docRefRe.FindAllStringSubmatch(instruction, -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		b, err := os.ReadFile(filepath.Join(docsDir, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&combined, "\n\n===== %s =====\n\n%s", name, scrubNUL(string(b)))
	}
	meta := map[string]json.RawMessage{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &meta)
	}
	// Unmarshalling explicit JSON null sets the map to nil — re-init before writing.
	if meta == nil {
		meta = map[string]json.RawMessage{}
	}
	if combined.Len() > 0 {
		doc, _ := json.Marshal(combined.String())
		meta["doc"] = doc
	}
	if len(meta) == 0 {
		return nil
	}
	out, _ := json.Marshal(meta)
	return out
}

// mergeSkillMetadata conservatively merges a skill's existing (already-stored)
// metadata with the freshly computed metadata for this seed pass: existing
// keys survive unless incoming explicitly provides that key. This is what
// stops a re-seed from wiping metadata.doc — the inlined playbook an
// Orchestra-origin row carries — or any other key (e.g. an extracted skill's
// status) that the current def file doesn't set. incoming keys always win
// when present, since an explicit def-provided value is the source of truth.
func mergeSkillMetadata(existing, incoming json.RawMessage) json.RawMessage {
	merged := map[string]json.RawMessage{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &merged)
	}
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	if len(incoming) > 0 {
		var inc map[string]json.RawMessage
		if json.Unmarshal(incoming, &inc) == nil {
			for k, v := range inc {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return incoming
	}
	return out
}

// enrichBundleItems reads each file-typed item's referenced doc and inlines its
// text under a "content" key, so the bundle can be loaded into a codex session at
// runtime. Paths are resolved relative to the KB repo root (<defsDir>/../..).
func enrichBundleItems(defsDir string, raw json.RawMessage) json.RawMessage {
	var items []map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return raw
	}
	root := filepath.Join(defsDir, "..", "..")
	for _, it := range items {
		p, _ := it["path"].(string)
		if p == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
			it["content"] = scrubNUL(string(b))
		}
	}
	out, err := json.Marshal(items)
	if err != nil {
		return raw
	}
	return out
}

type skillFile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Reasoning   string          `json:"reasoning"`
	Instruction string          `json:"instruction"`
	Metadata    json.RawMessage `json:"metadata"`
}

type contextFile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Items       json.RawMessage `json:"items"`
}

// FromDefsDir imports skills from <defsDir>/skills/*.json and context bundles
// from <defsDir>/context/*.json. Missing subdirectories are skipped. IDs default
// to the filename stem when absent.
func FromDefsDir(ctx context.Context, st *store.Store, defsDir string) (Result, error) {
	var res Result

	skillsDir := filepath.Join(defsDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(skillsDir, e.Name()))
			if err != nil {
				return res, fmt.Errorf("read %s: %w", e.Name(), err)
			}
			var sf skillFile
			if err := json.Unmarshal(b, &sf); err != nil {
				return res, fmt.Errorf("parse %s: %w", e.Name(), err)
			}
			if sf.ID == "" {
				sf.ID = strings.TrimSuffix(e.Name(), ".json")
			}
			if sf.Name == "" {
				sf.Name = sf.ID
			}
			// Load the full skill docs (the docs/*.md the instruction references) so
			// the whole skill can be loaded into a codex session, not just the pointer.
			meta := buildSkillMeta(defsDir, sf.Instruction, sf.Metadata)
			reasoning := sf.Reasoning

			// Re-seeding must never silently wipe data an existing row carries that
			// this def file doesn't know about — notably metadata.doc, the inlined
			// playbook an Orchestra-origin row holds (resolveSkillDocs feeds it to
			// codex), or any other key such as an extracted skill's status. Merge
			// conservatively: existing keys survive unless this def explicitly
			// provides them. Same for reasoning, which lives outside metadata.
			if existing, err := st.GetSkill(ctx, sf.ID); err == nil {
				meta = mergeSkillMetadata(existing.Metadata, meta)
				if reasoning == "" {
					reasoning = existing.Reasoning
				}
			}

			if err := st.UpsertSkill(ctx, store.Skill{
				ID: sf.ID, Name: sf.Name, Description: sf.Description, Category: sf.Category,
				Reasoning: reasoning, Instruction: sf.Instruction, Metadata: meta,
			}); err != nil {
				return res, fmt.Errorf("upsert skill %s: %w", sf.ID, err)
			}
			res.Skills++
		}
	}

	contextDir := filepath.Join(defsDir, "context")
	if entries, err := os.ReadDir(contextDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(contextDir, e.Name()))
			if err != nil {
				return res, fmt.Errorf("read %s: %w", e.Name(), err)
			}
			var cf contextFile
			if err := json.Unmarshal(b, &cf); err != nil {
				return res, fmt.Errorf("parse %s: %w", e.Name(), err)
			}
			if cf.ID == "" {
				cf.ID = strings.TrimSuffix(e.Name(), ".json")
			}
			if cf.Name == "" {
				cf.Name = cf.ID
			}
			// Enrich items with file CONTENT so the bundle can be loaded into a codex
			// session at runtime (Hyperplane imported the KB by reference, not value).
			items := enrichBundleItems(defsDir, cf.Items)
			if err := st.UpsertContextBundle(ctx, store.ContextBundle{
				ID: cf.ID, Name: cf.Name, Description: cf.Description, Items: items,
			}); err != nil {
				return res, fmt.Errorf("upsert context %s: %w", cf.ID, err)
			}
			res.ContextBundles++
		}
	}

	return res, nil
}
