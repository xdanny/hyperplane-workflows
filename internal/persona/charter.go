// Package persona makes the audit charters in personas/*.md first-class
// platform citizens: registered reviewers the platform can INVOKE, against a
// target it can hand them evidence for, with the verdict recorded where the
// human who decides will see it.
//
// # The design boundary
//
// An IN-PLATFORM persona review targets EVIDENCE the platform can hand a
// READ-ONLY agent:
//
//   - a run — its frozen SDK request, checkpoint, findings, file inventory and
//     bounded session-log tails;
//   - a skill or learned draft — its instruction, pinned evidence quotes,
//     support count, and the SDK check results of its supporting runs.
//
// A full browser UX audit — clicking through the app as Priya, finding the dead
// link, reading the copy on a card — stays ORCHESTRATOR-LEVEL: an agent driving
// a browser, which is not something a read-only codex call can do. Both paths
// share the same charter files, and personas/ stays the source of truth for
// both. This package never edits a charter; it reads one and asks it a
// question.
//
// # What a review is
//
// Advisory evidence. A persona never presses Approve, never gates anything, and
// never substitutes for the evalset requirement on a learned draft. It is a
// competent reader's judgement, recorded, next to the measured result — exactly
// the shape spec AI-12 asks for.
package persona

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Section headings a charter is expected to carry. They are STABLE by contract
// (personas/README.md: "The IDENTITY and RULES sections are stable"), which is
// what lets the prompt be assembled from the file rather than from a copy of it
// that would drift.
const (
	SectionIdentity     = "IDENTITY"
	SectionRules        = "RULES"
	SectionAuditProto   = "AUDIT PROTOCOL"
	SectionCompetence   = "COMPETENCE"
	SectionSkillReview  = "SKILL-REVIEW PROTOCOL"
	sectionHeadingLevel = "## "
)

// Competence is the parsed COMPETENCE section — the routing hook.
//
// Parsing is deliberately CONSERVATIVE. Only two things are treated as a
// machine-checkable claim of competence:
//
//   - Skills / Processes: the backticked ids the charter names outright. A
//     reviewer who wrote `postgres-data-modeling` in their own file is claiming
//     that skill, and nothing is being inferred on their behalf.
//   - Wording: set when the charter claims the WORDS of any instruction rather
//     than a named domain (Priya: "You review the words, in any skill
//     instruction or recipe copy"). That is a real, universal claim, and it is
//     the one the README says is "additive to, never a substitute for, a domain
//     review".
//
// Domains are the free-text bullets — "Anything that claims a row count, a
// rate, a grain, a key, or a load semantic". They are kept for DISPLAY and for
// a human choosing a reviewer. They are NOT matched against, because matching
// an English sentence to a skill id would manufacture competence nobody
// claimed, and a wrong reviewer is worse than no reviewer.
type Competence struct {
	Skills    []string `json:"skills,omitempty"`
	Processes []string `json:"processes,omitempty"`
	Domains   []string `json:"domains,omitempty"`
	Wording   bool     `json:"wording,omitempty"`

	// Bullets is Domains said the way the charter said it: one entry per
	// free-text COMPETENCE bullet, whole, with markdown emphasis and backticks
	// removed and nothing split further.
	//
	// Domains reduces each such bullet to its bold label, and rendering a list
	// of those joined by commas produced a lowercase run-on in which one claim's
	// own commas read as the boundary to the next ("anything that claims a row
	// count, a rate, a grain, a key, or a load semantic"). Bullets are what a
	// person reads; Domains stays the short form, and Skills/Processes/Wording
	// stay what routing matches on.
	Bullets []string `json:"bullets,omitempty"`
}

// Charter is one parsed persona file.
type Charter struct {
	ID         string
	Name       string
	Bar        string
	Body       string
	Version    string
	Source     string
	Competence Competence
	sections   map[string]string
}

// Section returns the body of one "## HEADING" section, or "" when the charter
// has no such section. Callers treat an absent section as "this persona has no
// protocol for that", never as a parse failure — a charter is a document, not a
// schema.
func (c Charter) Section(name string) string {
	return strings.TrimSpace(c.sections[name])
}

var (
	// h1Re matches the charter's title line.
	h1Re = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	// barRe lifts the reviewer's stated bar out of IDENTITY. The repo's
	// charters phrase it two ways: "Your bar: **…**" (the bar itself bolded)
	// and "**<Name>'s bar:** …" (the label bolded, the bar running to the end
	// of its paragraph — barLabelRe). A charter that states none simply has no
	// bar.
	barRe      = regexp.MustCompile(`Your bar[^:]*:\s*\*\*"?(.+?)"?\*\*`)
	barLabelRe = regexp.MustCompile(`(?s)\*\*[^*\n]*\bbar\b[^*\n]*:\s*\*\*\s*(.+?)(?:\n[ \t]*\n|\z)`)
	// tickRe pulls `backticked-ids` out of a competence bullet.
	tickRe = regexp.MustCompile("`([^`]+)`")
	// boldRe pulls the **bold label** that opens a competence bullet.
	boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// wordingRe detects the universal WORDS claim (Priya). It requires the
	// charter to say it about "any" instruction or copy — a domain reviewer who
	// merely mentions wording is not swept up.
	wordingRe = regexp.MustCompile(`(?i)review\s+\*\*the words\*\*|the words.{0,40}any skill instruction|any skill instruction or recipe copy`)
	// slugRe keeps a filename stem usable as an id.
	slugRe = regexp.MustCompile(`[^a-z0-9-]+`)
)

// Slug derives a persona id from a charter filename. The filename IS the id —
// personas/dev-ml-engineer.md is `dev-ml-engineer` — so a charter's identity
// cannot drift from the file a human edits.
func Slug(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(base), "-"), "-")
}

// Parse reads one charter file's content into a Charter.
//
// It never fails on missing sections: a charter with no COMPETENCE section is a
// registered reviewer with no claimed competence, which is honest. It fails
// only when the file has no title at all, because a reviewer with no name
// cannot be attributed.
func Parse(filename, content string) (Charter, error) {
	c := Charter{
		ID:       Slug(filename),
		Body:     content,
		Source:   filepath.Join("personas", filepath.Base(filename)),
		sections: splitSections(content),
	}
	if c.ID == "" {
		return c, fmt.Errorf("persona: %q has no usable id", filename)
	}
	m := h1Re.FindStringSubmatch(content)
	if m == nil {
		return c, fmt.Errorf("persona %s: no '# Title' heading — a reviewer with no name cannot be attributed", c.ID)
	}
	c.Name = strings.TrimSpace(m[1])
	if b := barRe.FindStringSubmatch(c.Section(SectionIdentity)); b != nil {
		c.Bar = strings.TrimSpace(b[1])
	} else if b := barLabelRe.FindStringSubmatch(c.Section(SectionIdentity)); b != nil {
		c.Bar = strings.Join(strings.Fields(b[1]), " ")
	}
	c.Competence = parseCompetence(c.Section(SectionCompetence))
	c.Version = hashCharter(content)
	return c, nil
}

// hashCharter is the charter's content version: a short sha256 prefix. It makes
// "this verdict was given against charter 9f2c1a…" a checkable statement, and
// it changes the moment anybody amends the file.
func hashCharter(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12]
}

// splitSections cuts the markdown at its "## " headings.
func splitSections(content string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(content, "\n")
	cur := ""
	var buf []string
	flush := func() {
		if cur != "" {
			out[cur] = strings.Join(buf, "\n")
		}
		buf = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, sectionHeadingLevel) && !strings.HasPrefix(line, "### ") {
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, sectionHeadingLevel))
			continue
		}
		if cur != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// parseCompetence reads the COMPETENCE section into the routing struct.
//
// The shape it expects is the one every charter in the repo uses: bullets whose
// **bold label** says what kind of claim it is, with backticked ids where the
// claim is a named thing. Anything it does not recognise becomes a domain
// string — visible, never matched on.
func parseCompetence(section string) Competence {
	var c Competence
	if section == "" {
		return c
	}
	if wordingRe.MatchString(section) {
		c.Wording = true
	}
	for _, bullet := range bullets(section) {
		label := ""
		if m := boldRe.FindStringSubmatch(bullet); m != nil {
			label = strings.ToLower(strings.TrimSpace(m[1]))
		}
		ids := uniqueLower(tickRe.FindAllStringSubmatch(bullet, -1))
		switch {
		case strings.HasPrefix(label, "skill"):
			c.Skills = append(c.Skills, ids...)
		case strings.Contains(label, "process") || strings.Contains(label, "recipe"):
			c.Processes = append(c.Processes, ids...)
		default:
			// A free-text claim. Kept WHOLE as well as reduced: the sentence is
			// what a person reads, and reducing it to its bold label is what
			// produced a lowercase run-on in which one claim's commas read as
			// the boundary to the next.
			if plain := plainBullet(bullet); plain != "" {
				c.Bullets = append(c.Bullets, plain)
			}
			// Keep the bold label when there is one (it is the reviewer's own
			// summary of the claim), else the bullet's first line.
			if label != "" {
				c.Domains = append(c.Domains, label)
			} else if first := firstLine(bullet); first != "" {
				c.Domains = append(c.Domains, first)
			}
			// Ids named inside a free-text bullet are still ids the reviewer
			// named. They are recorded as skills only when the bullet is not
			// obviously about something else, which the two cases above already
			// took; here they ride along as domains so nothing silently claims
			// competence the charter did not spell out under "Skills".
			c.Domains = append(c.Domains, ids...)
		}
	}
	c.Skills = dedupe(c.Skills)
	c.Processes = dedupe(c.Processes)
	c.Domains = dedupe(c.Domains)
	return c
}

// bullets splits a section into top-level "- " bullets, keeping continuation
// lines with their bullet (charters wrap at 80 columns).
func bullets(section string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.TrimSpace(strings.Join(cur, " ")))
			cur = nil
		}
	}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- "):
			flush()
			cur = append(cur, strings.TrimPrefix(trimmed, "- "))
		case trimmed == "":
			flush()
		case len(cur) > 0:
			cur = append(cur, trimmed)
		}
	}
	flush()
	return out
}

// plainBullet renders one COMPETENCE bullet as the sentence a person reads:
// markdown emphasis and backticks dropped, wrapped lines rejoined, the text
// otherwise exactly as the charter wrote it. Commas inside it are the author's
// punctuation, never separators — which is the whole point of keeping it.
func plainBullet(bullet string) string {
	s := strings.NewReplacer("**", "", "`", "").Replace(bullet)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(strings.Trim(s, "*")))
}

func uniqueLower(matches [][]string) []string {
	var out []string
	for _, m := range matches {
		v := strings.ToLower(strings.TrimSpace(m[1]))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ToStore projects a parsed charter onto its registry row.
func (c Charter) ToStore() (store.Persona, error) {
	b, err := json.Marshal(c.Competence)
	if err != nil {
		return store.Persona{}, fmt.Errorf("persona %s: encode competence: %w", c.ID, err)
	}
	return store.Persona{
		ID:         c.ID,
		Name:       c.Name,
		Bar:        c.Bar,
		Charter:    c.Body,
		Competence: b,
		Version:    c.Version,
		Source:     c.Source,
	}, nil
}

// FromStore re-parses a registry row back into a Charter, so the review path
// works from the stored charter (what the platform actually holds) rather than
// from the file on disk (which may have moved on).
func FromStore(p store.Persona) Charter {
	c, err := Parse(p.ID+".md", p.Charter)
	if err != nil {
		// A row with an unparseable charter still names a reviewer; fall back to
		// the stored columns rather than losing the persona entirely.
		c = Charter{ID: p.ID, Body: p.Charter, sections: splitSections(p.Charter)}
	}
	c.ID = p.ID
	if p.Name != "" {
		c.Name = p.Name
	}
	if p.Bar != "" {
		c.Bar = p.Bar
	}
	if p.Version != "" {
		c.Version = p.Version
	}
	if len(p.Competence) > 0 {
		var comp Competence
		if json.Unmarshal(p.Competence, &comp) == nil {
			c.Competence = comp
		}
	}
	return c
}
