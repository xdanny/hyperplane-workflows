package persona

// The DISPLAY half of the competence parse.
//
// charter_test.go covers what routing matches on. This covers what a person
// reads: a free-text claim is kept as the charter wrote it, so the commas
// inside one claim are punctuation rather than the boundary to the next.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bulleted ids (Skills / Recipes) are routed on and are deliberately NOT
// repeated as display bullets — they are shown under "competent for", never
// under "also says it can judge (not used to route reviews)", which would be a
// false label for a claim that IS routed on.
func TestCompetenceBulletsAreWholeSentences(t *testing.T) {
	section := `
- **Skills**: ` + "`postgres-data-modeling`" + `.
- **Anything that claims a row count, a rate, a grain, a key, or a load
  semantic**, wherever it appears.
`
	c := parseCompetence(section)

	if len(c.Bullets) != 1 {
		t.Fatalf("only the free-text bullet is a display claim, got %d: %q", len(c.Bullets), c.Bullets)
	}
	want := "Anything that claims a row count, a rate, a grain, a key, or a load semantic, wherever it appears."
	if c.Bullets[0] != want {
		t.Errorf("bullet must be the charter's whole sentence, unwrapped and unemphasised:\n got %q\nwant %q", c.Bullets[0], want)
	}
	if strings.Contains(c.Bullets[0], "**") || strings.Contains(c.Bullets[0], "`") {
		t.Error("markdown emphasis and backticks must not reach the page")
	}
}

// The same guarantee against the charters that actually ship: every persona
// with a free-text claim has a whole sentence to render for it.
func TestRealChartersCarryDisplayBullets(t *testing.T) {
	dir := repoPersonasDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read personas dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		c, err := Parse(e.Name(), string(body))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		if len(c.Competence.Domains) == 0 {
			continue
		}
		if len(c.Competence.Bullets) == 0 {
			t.Errorf("%s: has free-text competence but nothing to render", e.Name())
			continue
		}
		for _, b := range c.Competence.Bullets {
			// A bare bold label ("jargon") is what the run-on was made of; a
			// rendered claim is a sentence.
			if len(strings.Fields(b)) < 4 {
				t.Errorf("%s: bullet %q is a label, not a claim", e.Name(), b)
			}
		}
	}
}
