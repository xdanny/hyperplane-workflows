package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoPersonasDir locates the real personas/ directory, so the parser is tested
// against the charters that actually ship rather than a fixture that could
// drift away from them.
func repoPersonasDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		cand := filepath.Join(dir, "personas")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("personas/ not found from the test working directory")
	return ""
}

// TestParseRealCharters asserts every shipped charter parses into a usable
// reviewer: an id from the filename, a name, a bar, and the sections the review
// prompt is assembled from. A charter that stopped parsing would silently
// degrade every review run against it.
func TestParseRealCharters(t *testing.T) {
	dir := repoPersonasDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == readmeName {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		c, err := Parse(e.Name(), string(b))
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		seen++
		if c.ID != strings.TrimSuffix(e.Name(), ".md") {
			t.Errorf("%s: id %q does not match the filename", e.Name(), c.ID)
		}
		if c.Name == "" {
			t.Errorf("%s: no name", e.Name())
		}
		if c.Bar == "" {
			t.Errorf("%s: no bar lifted from IDENTITY — the charter states one", e.Name())
		}
		if c.Version == "" {
			t.Errorf("%s: no content version", e.Name())
		}
		for _, section := range []string{SectionIdentity, SectionRules, SectionCompetence, SectionSkillReview} {
			if c.Section(section) == "" {
				t.Errorf("%s: section %q is empty — the review prompt needs it", e.Name(), section)
			}
		}
	}
	if seen < 4 {
		t.Fatalf("expected the four shipped charters, parsed %d", seen)
	}
}

// TestCompetenceParsingIsConservative pins the parsing rule the whole routing
// hook rests on: only ids a charter NAMED become machine-checkable competence.
// Free-text domain prose is kept for display and never routed on, because a
// plausible-looking wrong reviewer is worse than no reviewer.
func TestCompetenceParsingIsConservative(t *testing.T) {
	section := `
You review, and are qualified to judge:

- **Skills**: ` + "`postgres-data-modeling`, `api-design`" + `.
- **Recipes and processes**: ` + "`data-pipeline`" + `, and ` + "`dataset-shift-audit`" + ` when it
  lands — including their contracts and fixtures.
- **Anything that claims a row count, a rate, a grain, a key, or a load
  semantic**, wherever it appears.
`
	c := parseCompetence(section)

	if got := strings.Join(c.Skills, ","); got != "postgres-data-modeling,api-design" {
		t.Errorf("skills = %q", got)
	}
	if got := strings.Join(c.Processes, ","); got != "data-pipeline,dataset-shift-audit" {
		t.Errorf("processes = %q", got)
	}
	if c.Wording {
		t.Error("a domain reviewer must not be given the universal wording claim")
	}
	if len(c.Domains) == 0 {
		t.Error("the free-text bullet should be kept for display")
	}

	// The free-text bullet mentions "a row count" — a skill called row-count must
	// still NOT match, because the charter never named it.
	if ok, _ := Match(c, Target{Kind: TargetSkill, ID: "row-count"}); ok {
		t.Error("free-text domain prose must never manufacture competence")
	}
	if ok, why := Match(c, Target{Kind: TargetSkill, ID: "api-design"}); !ok || why == "" {
		t.Errorf("a named skill must match with a reason: ok=%v why=%q", ok, why)
	}
	if ok, _ := Match(c, Target{Kind: TargetSkill, ID: "data-pipeline"}); !ok {
		t.Error("a named process must match")
	}
	// Run targets are never routed by competence — a human picks the reviewer.
	if ok, _ := Match(c, Target{Kind: TargetRun, ID: "20260820-1"}); ok {
		t.Error("run targets must not be routed by skill competence")
	}
}

// TestWordingCompetenceIsUniversal covers Priya: a charter that claims the
// WORDS of any instruction is competent everywhere, and says why.
func TestWordingCompetenceIsUniversal(t *testing.T) {
	c := parseCompetence("You review **the words**, in any skill instruction or recipe copy — never the method.\n\n" +
		"- **Readability** — could a non-engineer follow this?\n")
	if !c.Wording {
		t.Fatal("the universal wording claim was not detected")
	}
	ok, why := Match(c, Target{Kind: TargetSkill, ID: "anything-at-all"})
	if !ok {
		t.Fatal("a wording reviewer is competent over any skill's instruction")
	}
	if !strings.Contains(why, "never a substitute") {
		t.Errorf("the reason must say it is additive, not a substitute: %q", why)
	}
}

// TestParseTarget covers the two accepted forms and the refusals. An unknown
// target kind must be rejected rather than guessed: the alternative is
// assembling a review prompt out of evidence nobody collected.
func TestParseTarget(t *testing.T) {
	for _, in := range []string{"run:20260820-101015.476-pr", "skill:ai-engineering"} {
		if _, err := ParseTarget(in); err != nil {
			t.Errorf("ParseTarget(%q): %v", in, err)
		}
	}
	for _, in := range []string{"", "run", "run:", "browser:localhost", "skill:../etc/passwd", "skill:a/b"} {
		if _, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) should have been refused", in)
		}
	}
}

// TestVersionTracksContent asserts the charter hash changes when the file does
// — what makes "this verdict was given against charter X" checkable.
func TestVersionTracksContent(t *testing.T) {
	a, err := Parse("x.md", "# X\n\n## IDENTITY\n\nYou are X.\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("x.md", "# X\n\n## IDENTITY\n\nYou are X, amended.\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.Version == b.Version {
		t.Fatal("an amended charter must get a new version")
	}
}
