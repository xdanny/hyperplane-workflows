package brief

// The rendered document is both the model's input and its numeric grounding source.
// Execution, requested checks, model reviews and human acceptance remain distinct.

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxVerdictBytes caps one node's verdict text inside the state document.
	// Truncation is stated in the rendered document.
	maxVerdictBytes = 6 * 1024
	// maxFailingNodes bounds the failing nodes described.
	maxFailingNodes = 12
	// maxCriteria bounds the criteria listed.
	maxCriteria = 40
	// maxSlots bounds the recipe slots echoed.
	maxSlots = 20
)

// StateDoc is everything the platform can say about one subject, from its own
// records, after the subject finished.
type StateDoc struct {
	// Kind is the subject: store.BriefSubjectRun / Delivery / Project.
	Kind      string
	SubjectID string
	Project   string

	// ── what it was ────────────────────────────────────────────────────────
	RunID string
	DAG   string
	Goal  string
	// Recipe is the template a run was instantiated from, with its own
	// description as Purpose. Empty on a run nobody instantiated from a recipe.
	//
	// Recipe is a UUID. RecipeName is what to CALL it, from the one recipe-name
	// resolver — without it the brief opened "This workflow ran 1979dd1f-…",
	// which names the work to nobody.
	Recipe        string
	RecipeName    string
	RecipePurpose string
	Slots         []Slot

	// ── what happened ──────────────────────────────────────────────────────
	// State is the run's terminal execution state, not a quality judgement.
	State string
	// Rehearsal is true when the run was a dry run: nothing real was executed,
	// so nothing about it may be reported as delivered work.
	Rehearsal bool
	StartedAt time.Time
	EndedAt   time.Time
	// FailingNode is the first failed/blocked node, the one a reader means by
	// "where did it stop".
	FailingNode   string
	Failures      []Failure
	Phase         string
	ContinuedFrom string
	Criteria      []Criterion
	Delivery      *Delivery

	// Notes are honest statements about what this document does NOT contain.
	Notes []string

	// Sources are the prior briefs a PROJECT narrative is written from.
	Sources []Source
	// ClosedLines are the project's delivery lines that ended WITHOUT an
	// acceptance. They are not what the project built, and they are not
	// nothing: a line closed after five attempts usually leaves a hardened
	// recipe, a contract contradiction fixed for every future run, and a set of
	// merged lessons. Omitting them made "what this project has built so far"
	// silently exclude five of the project's seven runs.
	ClosedLines []ClosedLine
}

// ClosedLine is one delivery line that never reached an acceptance.
type ClosedLine struct {
	// Name is what to call the line — its newest attempt's goal, recipe or DAG.
	Name string
	// RunID is the newest attempt.
	RunID string
	// Attempts is how many attempts the line took.
	Attempts int
	// State is the newest attempt's terminal execution state.
	State string
	// Verdict is the human's recorded decision, already in plain words, or
	// empty when nobody recorded one.
	Verdict string
	// Reason is one line from the newest attempt's own brief, when it has one.
	Reason string
}

// Slot is one filled context slot of the recipe the run came from.
type Slot struct {
	Name  string
	Value string
}

// Failure is the current SDK checkpoint execution problem.
type Failure struct {
	NodeID string
	Kind   string
	State  string
	Error  string
	// Verdict is a recorded SDK finding, capped at maxVerdictBytes.
	Verdict          string
	VerdictTruncated bool
}

// Criterion is an explicitly requested program check or model review.
type Criterion struct {
	ID         string
	Title      string
	Basis      string
	ProofNode  string
	ProofState string
	Proven     bool
	// Rehearsal is true when the run was a dry run, so this criterion's "passed"
	// node was a simulation. Carried per criterion, not only in the header,
	// because the criteria list is the part a reader quotes.
	Rehearsal bool
}

// Delivery is the human's recorded verdict on a run.
type Delivery struct {
	DecisionID string
	Verdict    string
	Note       string
	DecidedAt  time.Time
}

// Source is one earlier brief a project narrative is written from.
type Source struct {
	SubjectID string
	Verdict   string
	When      time.Time
	// Body is the earlier brief's own text, already rendered to prose.
	Body string
	// Grounded carries the earlier brief's honesty flag forward: a narrative
	// built on an unverified brief must not present it as checked.
	Grounded bool
}

// ProvenCount tallies the criteria.
func (d StateDoc) ProvenCount() (total, proven int) {
	for _, c := range d.Criteria {
		total++
		if c.Proven {
			proven++
		}
	}
	return total, proven
}

// Text renders the state document. This exact string is what the engine reads
// and what the grounding check searches — see the file header.
func (d StateDoc) Text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "=== SUBJECT ===\nkind: %s\nid: %s\n", d.Kind, d.SubjectID)
	if d.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", d.Project)
	}

	if d.Kind == "project" {
		d.writeProject(&b)
		d.writeNotes(&b)
		return b.String()
	}

	// ── what this workflow is ──────────────────────────────────────────────
	b.WriteString("\n=== WHAT THIS WORKFLOW IS ===\n")
	fmt.Fprintf(&b, "workflow: %s\n", orNotRecorded(d.DAG))
	if d.Goal != "" {
		fmt.Fprintf(&b, "what was asked for: %s\n", d.Goal)
	} else {
		b.WriteString("what was asked for: not recorded\n")
	}
	if d.Recipe != "" {
		// The NAME on the line the engine writes prose from; the id on its own
		// line, so nothing that needs to match the run doc loses it.
		fmt.Fprintf(&b, "recipe: %s\n", recipeLabel(d))
		if d.RecipeName != "" {
			fmt.Fprintf(&b, "recipe id: %s\n", d.Recipe)
		}
		if d.RecipePurpose != "" {
			fmt.Fprintf(&b, "recipe purpose: %s\n", d.RecipePurpose)
		}
	} else {
		b.WriteString("recipe: none — this run was not instantiated from a recipe\n")
	}
	if len(d.Slots) > 0 {
		b.WriteString("context the recipe was given:\n")
		for i, s := range d.Slots {
			if i >= maxSlots {
				fmt.Fprintf(&b, "  … and %d more slot(s), not listed\n", len(d.Slots)-maxSlots)
				break
			}
			fmt.Fprintf(&b, "  - %s: %s\n", s.Name, collapse(s.Value, 400))
		}
	}

	// ── what happened ──────────────────────────────────────────────────────
	b.WriteString("\n=== SDK EXECUTION STATE (completion is not a quality judgement) ===\n")
	fmt.Fprintf(&b, "run: %s\nfinal state: %s\n", orNotRecorded(d.RunID), orNotRecorded(d.State))
	if d.Rehearsal {
		b.WriteString("mode: REHEARSAL (dry run) — no real work was executed and nothing here was delivered\n")
	}
	if !d.StartedAt.IsZero() {
		fmt.Fprintf(&b, "started: %s\n", d.StartedAt.UTC().Format(time.RFC3339))
	}
	if !d.EndedAt.IsZero() {
		fmt.Fprintf(&b, "ended: %s\n", d.EndedAt.UTC().Format(time.RFC3339))
	}
	if d.FailingNode != "" {
		fmt.Fprintf(&b, "stopped at step: %s\n", d.FailingNode)
	} else {
		b.WriteString("stopped at step: not recorded in this checkpoint\n")
	}
	if d.Phase != "" {
		fmt.Fprintf(&b, "SDK phase: %s\n", d.Phase)
	}
	if d.ContinuedFrom != "" {
		fmt.Fprintf(&b, "continues run: %s (only this run's checkpoint is described)\n", d.ContinuedFrom)
	}

	total, passed := d.ProvenCount()
	b.WriteString("\n=== REQUESTED CHECKS AND MODEL REVIEWS ===\n")
	b.WriteString("Program checks verify their configured conditions. Model reviews are fallible judgments. Neither alone is human acceptance or a guarantee of complete correctness.\n")
	if total == 0 {
		b.WriteString("No extra check or model review is recorded. Completion alone does not establish independently checked correctness.\n")
	} else {
		if d.Rehearsal {
			fmt.Fprintf(&b, "%d of %d checks/reviews marked passed by stand-ins; this rehearsal provides no delivery evidence.\n", passed, total)
		} else {
			fmt.Fprintf(&b, "%d of %d requested checks/reviews passed; %d did not pass.\n", passed, total, total-passed)
		}
		for i, c := range d.Criteria {
			if i >= maxCriteria {
				fmt.Fprintf(&b, "… and %d more checks/reviews, not listed\n", len(d.Criteria)-maxCriteria)
				break
			}
			status := "NOT CHECKED (no passing result recorded)"
			if c.Proven && c.Rehearsal {
				status = "SIMULATED PASS"
			} else if c.Proven {
				status = "PASSED"
			} else if c.ProofState != "" {
				status = "NOT PASSED — " + c.ProofState
			}
			fmt.Fprintf(&b, "- [%s] %s\n  requested check/review: %s\n  basis: %s\n  step: %s (state %s)\n", c.ID, status, orNotRecorded(c.Title), orNotRecorded(c.Basis), orNotRecorded(c.ProofNode), orNotRecorded(c.ProofState))
		}
	}

	// ── what failed ────────────────────────────────────────────────────────
	b.WriteString("\n=== RECORDED EXECUTION PROBLEM ===\n")
	if len(d.Failures) == 0 {
		b.WriteString("No execution problem is recorded in this checkpoint.\n")
	}
	for i, f := range d.Failures {
		if i >= maxFailingNodes {
			n := len(d.Failures) - maxFailingNodes
			fmt.Fprintf(&b, "\n… and %d more stopped %s, not listed\n", n, plural(n, "step", "steps"))
			break
		}
		fmt.Fprintf(&b, "\n- step %s [kind=%s state=%s", f.NodeID, orNotRecorded(f.Kind), f.State)
		b.WriteString("]\n")
		if f.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", collapse(f.Error, 600))
		}
		if f.Verdict != "" {
			fmt.Fprintf(&b, "  checkpoint finding:\n%s\n", indent(f.Verdict, "    "))
			if f.VerdictTruncated {
				b.WriteString("    (this verdict was truncated — it is longer than the state document can carry)\n")
			}
		}
	}

	// ── delivery decision ──────────────────────────────────────────────────
	b.WriteString("\n=== DELIVERY DECISION (a HUMAN's verdict — the only status here that means somebody was satisfied) ===\n")
	if d.Delivery == nil && d.Rehearsal {
		// The model is writing "what to do next" from this document, and on a
		// rehearsal there is exactly one honest answer. Told plainly here, the
		// brief cannot ask for a verdict the run page refuses to take (Q-41b).
		b.WriteString("No decision recorded, and none can be: this was a REHEARSAL. " +
			"Both outcome endpoints refuse an acceptance on a dry run. " +
			"The only next step to state is: " + RehearsalNextStepDoc + "\n")
	} else if d.Delivery == nil {
		b.WriteString("No decision recorded. Nobody has accepted or rejected this delivery yet.\n")
	} else {
		fmt.Fprintf(&b, "verdict: %s\n", humanVerdict(d.Delivery.Verdict))
		if !d.Delivery.DecidedAt.IsZero() {
			fmt.Fprintf(&b, "decided: %s\n", d.Delivery.DecidedAt.UTC().Format(time.RFC3339))
		}
		if strings.TrimSpace(d.Delivery.Note) != "" {
			fmt.Fprintf(&b, "the person's note, verbatim: %s\n", d.Delivery.Note)
		} else {
			b.WriteString("the person left no note.\n")
		}
	}

	d.writeNotes(&b)
	return b.String()
}

// writeProject renders the source briefs a project narrative is built from.
func (d StateDoc) writeProject(b *strings.Builder) {
	b.WriteString("\n=== THE ACCEPTED DELIVERIES OF THIS PROJECT, NEWEST FIRST ===\n")
	b.WriteString("Each block below is a brief this platform already wrote about one accepted delivery.\n")
	if len(d.Sources) == 0 {
		b.WriteString("\nNone. This project has no accepted delivery with a brief.\n")
		return
	}
	for i, s := range d.Sources {
		fmt.Fprintf(b, "\n--- delivery %d: %s", i+1, s.SubjectID)
		if s.Verdict != "" {
			fmt.Fprintf(b, " (%s)", s.Verdict)
		}
		if !s.When.IsZero() {
			fmt.Fprintf(b, " on %s", s.When.UTC().Format("2006-01-02"))
		}
		b.WriteString(" ---\n")
		if !s.Grounded {
			b.WriteString("(the numbers in this earlier brief were NOT verified against the platform's records)\n")
		}
		b.WriteString(strings.TrimSpace(s.Body))
		b.WriteString("\n")
	}
	d.writeClosedLines(b)
}

// writeClosedLines renders the delivery lines that ended without an acceptance.
//
// They are listed AFTER the accepted deliveries and under a heading that says
// what they are, so the narrative can neither present them as things the
// project shipped nor pretend the project never attempted them.
func (d StateDoc) writeClosedLines(b *strings.Builder) {
	b.WriteString("\n=== DELIVERY LINES THAT CLOSED WITHOUT AN ACCEPTANCE ===\n")
	b.WriteString("These are NOT things this project delivered. They are part of what it has done, and what usually remains from one is the hardened recipe and the contract lessons merged from it.\n")
	if len(d.ClosedLines) == 0 {
		b.WriteString("\nNone. Every delivery line in this project reached an accepted delivery.\n")
		return
	}
	for _, c := range d.ClosedLines {
		fmt.Fprintf(b, "\n- line: %s (newest attempt %s)\n", orNotRecorded(c.Name), c.RunID)
		fmt.Fprintf(b, "  attempts: %d\n", c.Attempts)
		fmt.Fprintf(b, "  newest attempt's final run state: %s\n", orNotRecorded(c.State))
		if c.Verdict != "" {
			fmt.Fprintf(b, "  the human's delivery decision: %s\n", c.Verdict)
		} else {
			b.WriteString("  the human's delivery decision: none recorded — this line was never decided\n")
		}
		if c.Reason != "" {
			fmt.Fprintf(b, "  from the newest attempt's own brief: %s\n", collapse(c.Reason, 400))
		} else {
			b.WriteString("  from the newest attempt's own brief: not recorded — no brief was written for it\n")
		}
	}
}

func (d StateDoc) writeNotes(b *strings.Builder) {
	b.WriteString("\n=== WHAT THIS DOCUMENT DOES NOT CONTAIN ===\n")
	b.WriteString("- The contents of the files the run produced. Their names are evidence of existence, nothing more.\n")
	// The relevance caveat is on the line itself, not only in the writing
	// contract. This list is the whole record the generator sees, and an
	// unqualified "not recorded" here was faithfully reported in every brief —
	// including on a data-audit deliverable that has no app to present.
	b.WriteString("- Anything about how this is presented in the app, or how it behaves when used. " +
		"Mention this ONLY where the deliverable is something a person uses in an app; on a report, " +
		"a scorecard or an audit it is not an absence any reader would have assumed away.\n")
	for _, n := range d.Notes {
		fmt.Fprintf(b, "- %s\n", n)
	}
}

// ── small helpers ───────────────────────────────────────────────────────────

// humanVerdict spells a decision enum the way a person says it.
//
// `changes_requested` is a database value, and a brief that copied it verbatim
// wrote "The human's delivery decision was changes_requested." into prose a
// colleague reads. The writing contract now says to expand inherited tokens,
// but the cheaper fix is upstream of the model: never show it the token. The
// state document is the only thing the generator sees, so what it says here is
// what a brief can quote — and an unknown value passes through untouched rather
// than being guessed at.
func humanVerdict(v string) string {
	switch strings.TrimSpace(v) {
	case "changes_requested":
		return "changes requested"
	case "accepted":
		return "accepted"
	case "rejected":
		return "rejected"
	}
	return orNotRecorded(v)
}

func orNotRecorded(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not recorded"
	}
	return s
}

// collapse squeezes whitespace and caps at n bytes on a rune boundary.
func collapse(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// cap trims to maxVerdictBytes on a rune boundary, reporting whether it bit.
// A cut mid-rune is garbage in a prompt and invalid UTF-8 in Postgres.
func capText(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) <= maxVerdictBytes {
		return s, false
	}
	n := maxVerdictBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]), true
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// sortedKeys returns a map's keys in a stable order, so the same facts always
// render as the same document — an inputs digest that changed between two
// identical states would be worthless.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
