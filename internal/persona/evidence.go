package persona

// evidence.go — what the platform can hand a read-only reviewer.
//
// Two shapes, because there are exactly two review targets:
//
//   - a SKILL (or learned draft): its instruction in full, its support model,
//     its pinned evidence quotes, and the SDK checkpoints of the runs it was
//     learned from. That is the material the SKILL-REVIEW PROTOCOL in every
//     charter asks for by name.
//   - a RUN: what it promised, what it proved, what failed, what it produced,
//     and the tail of how it worked. Assembled by the API from the collectors
//     that already serve the Delivery view, so the reviewer reads the same
//     evidence a human does rather than a second, divergent digest.
//
// Everything here is bounded and says when a bound bit. A reviewer who is shown
// a truncated list as if it were complete is being lied to.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/rundocs"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const (
	// maxSupportRuns bounds how many supporting runs contribute SDK evidence.
	maxSupportRuns = 4
	// maxRunEvidenceNodes bounds the declared SDK steps described per run.
	maxRunEvidenceNodes = 30
	// maxArtifactsListed bounds the artifact inventory.
	maxArtifactsListed = 40
	// maxLogTail caps one node's log tail, and maxLogNodes how many contribute.
	maxLogTail  = 2000
	maxLogNodes = 8
)

// SkillEvidence assembles the material for a skill or learned-draft review.
func SkillEvidence(ctx context.Context, st *store.Store, skillID string) (Evidence, error) {
	sk, err := st.GetSkill(ctx, skillID)
	if err != nil {
		return Evidence{}, err
	}
	ev := Evidence{
		Target: Target{Kind: TargetSkill, ID: sk.ID},
		Title:  fmt.Sprintf("%s (%s)", sk.Name, statusOf(sk)),
		Note: "You are reading the skill as the platform stores it. You cannot see how it " +
			"behaves in a prompt, and no evalset result is attached unless one is quoted below.",
	}

	meta := map[string]any{}
	if len(sk.Metadata) > 0 {
		_ = json.Unmarshal(sk.Metadata, &meta)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "id: %s\nname: %s\nstatus: %s\n", sk.ID, sk.Name, statusOf(sk))
	if sk.Category != "" {
		fmt.Fprintf(&sb, "category: %s\n", sk.Category)
	}
	if sk.Description != "" {
		fmt.Fprintf(&sb, "description: %s\n", sk.Description)
	}
	runs := metaStrings(meta, "sourceRuns")
	if len(runs) == 0 {
		if one, ok := meta["sourceRun"].(string); ok && one != "" {
			runs = []string{one}
		}
	}
	if len(runs) == 0 {
		sb.WriteString("support: hand-written — not extracted from any run.\n")
	} else {
		fmt.Fprintf(&sb, "support: %d run(s) — %s\n", len(runs), strings.Join(runs, ", "))
		if len(runs) == 1 {
			sb.WriteString("NOTE: one supporting run. The platform's own label for this is " +
				"\"extracted from a single run — treat it as an anecdote until another run supports it\".\n")
		}
	}
	// The self-reported confidence is included and LABELLED as what it is. It is
	// deliberately not shown on a card (a bare scalar reads as rigour it has not
	// earned), but a reviewer whose bar is "evidence or theater" should be told
	// the number exists and where it came from.
	if conf, ok := meta["confidence"].(float64); ok {
		fmt.Fprintf(&sb, "self-reported confidence: %.2f — the extraction model's opinion of itself, not a measurement.\n", conf)
	}
	if r, ok := meta["rationale"].(string); ok && r != "" {
		fmt.Fprintf(&sb, "extraction rationale: %s\n", r)
	}
	if d, ok := meta["sourceDelivery"].(string); ok && d != "" {
		fmt.Fprintf(&sb, "learned from the accepted delivery: %s\n", d)
	}
	if _, ok := meta["approvedWithoutEvalset"]; ok {
		sb.WriteString("this skill was approved WITHOUT an evalset (the documented override was used).\n")
	}
	ev.Sections = append(ev.Sections, Section{Heading: "SKILL METADATA AND SUPPORT", Body: sb.String()})

	ev.Sections = append(ev.Sections, Section{
		Heading: "INSTRUCTION (in full — this is what enters every prompt that resolves the skill)",
		Body:    sk.Instruction,
	})
	if doc, ok := meta["doc"].(string); ok && strings.TrimSpace(doc) != "" {
		ev.Sections = append(ev.Sections, Section{Heading: "DOC (the fuller reference loaded alongside the instruction)", Body: doc})
	}

	// Pinned evidence: the quotes extraction said were load-bearing. A draft with
	// none says so rather than showing an empty block — "extracted before
	// evidence pinning existed" is a fact about the draft a reviewer needs.
	var eb strings.Builder
	quotes := metaEvidence(meta)
	if len(quotes) == 0 {
		eb.WriteString("No pinned evidence quotes. Nothing in this skill points back at a line in a run; " +
			"its grounds can only be read in the runs themselves.\n")
	}
	for i, q := range quotes {
		fmt.Fprintf(&eb, "%d. run %s, node %s:\n   %q\n", i+1, orDash(q.RunID), q.NodeID, q.Quote)
	}
	ev.Sections = append(ev.Sections, Section{Heading: "PINNED EVIDENCE QUOTES", Body: eb.String()})

	// Supporting evidence is local to each source run. A descendant or a later
	// continuation cannot retroactively verify the run that supplied the skill.
	var gb strings.Builder
	if len(runs) == 0 {
		gb.WriteString("(no supporting runs — no execution evidence is attached)\n")
	}
	for i, runID := range runs {
		if i >= maxSupportRuns {
			fmt.Fprintf(&gb, "\n… and %d more supporting run(s), not listed here.\n", len(runs)-maxSupportRuns)
			break
		}
		fmt.Fprintf(&gb, "\nrun %s:\n", runID)
		run, err := st.GetRun(ctx, runID)
		if err != nil {
			gb.WriteString("  (this source run is no longer readable)\n")
			continue
		}
		gb.WriteString(sdkRunSummary(run))
	}
	ev.Sections = append(ev.Sections, Section{Heading: "SDK EVIDENCE OF THE SUPPORTING RUNS", Body: gb.String()})

	return ev, nil
}

// RunFacts is what the API collected about a run, before it becomes prompt text.
// It is a plain struct so the collectors stay where they are (internal/httpapi,
// serving the Delivery view) and this package stays free of HTTP.
type RunFacts struct {
	Run store.Run
	// Failures contains recorded SDK checkpoint findings for a failed or interrupted run.
	Failures json.RawMessage
	// Artifacts are the file names the run produced.
	Artifacts []string
	// ArtifactsTruncated says the inventory was cut.
	ArtifactsTruncated bool
	// Documents are the produced documents themselves, with their text. An
	// empty slice means none were recorded or none could be read — never that
	// the reviewer should assume the run produced nothing.
	Documents []rundocs.Document
	// Logs are capped tails of the nodes' working logs.
	Logs []NodeTail
}

// NodeTail is the tail of one node's working log.
type NodeTail struct {
	RunID  string
	NodeID string
	Tail   string
}

// RunEvidence renders the collected facts into review material.
func RunEvidence(f RunFacts) Evidence {
	run := f.Run
	ev := Evidence{
		Target: Target{Kind: TargetRun, ID: run.RunID},
		Title:  fmt.Sprintf("workflow %s — state %s", run.DAG, run.State),
		Note: "You are reading the run's recorded evidence, not the app. You cannot click " +
			"anything, re-run anything, or see how this is presented in the UI. Judge what the " +
			"record supports, and say which of your protocol goals you could not run. Completion " +
			"alone does not prove correctness. Extra checks are optional; not_requested means " +
			"no independent check was requested, and an absent verification field makes no claim.",
	}
	// What the reviewer was actually handed, said before the first section. A
	// reviewer that reported "the full note is unavailable to assess" about a
	// run whose note it had never been sent was not being cautious — it was
	// being accurate about a prompt that omitted the work (S-15, D-14).
	if len(f.Documents) > 0 {
		ev.Note += " You HAVE been given the documents this run produced, quoted in full below unless a " +
			"section says it was truncated — read: " + strings.Join(rundocs.Paths(f.Documents), ", ") +
			". Judge what they say; do not report them as unavailable."
	} else {
		ev.Note += " No document this run produced could be read. Say so plainly, and do not pass or " +
			"fail the WORK on evidence you were not given — judge only what the records below support."
	}

	var rb strings.Builder
	fmt.Fprintf(&rb, "run: %s\nworkflow: %s\nstate: %s\n", run.RunID, run.DAG, run.State)
	if run.Type != "" {
		fmt.Fprintf(&rb, "type: %s\n", run.Type)
	}
	if run.Project != "" {
		fmt.Fprintf(&rb, "project: %s\n", run.Project)
	}
	if run.Goal != "" {
		fmt.Fprintf(&rb, "goal: %s\n", run.Goal)
	}
	ev.Sections = append(ev.Sections, Section{Heading: "THE REQUEST", Body: rb.String()})

	// One section per declared step, headed by the step's own name. They used to
	// share a single heading, so every citation a reviewer pinned came back as
	// "SDK STEP COMPLETION AND CONFIGURED CHECKS" — six identical evidence rows
	// that located nothing (S-15).
	ev.Sections = append(ev.Sections, sdkRunSections(run)...)

	// Checkpoint findings are recorded facts, not reconstructed graph verdicts.
	if len(f.Failures) > 0 {
		ev.Sections = append(ev.Sections, Section{
			Heading: "WHAT FAILED (recorded SDK checkpoint, JSON)",
			Body:    string(f.Failures),
		})
	} else {
		ev.Sections = append(ev.Sections, Section{
			Heading: "WHAT FAILED",
			Body:    "No failed SDK checkpoint findings were supplied. This does not independently establish correctness.",
		})
	}

	// The deliverables, in full, before the inventory: a reviewer reads the work
	// and then the list of what else sits beside it, not the other way round.
	for _, doc := range f.Documents {
		body := doc.Text
		if doc.Truncated {
			body += "\n\n(TRUNCATED — this document is longer than the evidence bound; the end is not shown.)\n"
		}
		ev.Sections = append(ev.Sections, Section{Heading: "PRODUCED DOCUMENT: " + doc.Caption(), Body: body})
	}

	var ab strings.Builder
	if len(f.Artifacts) == 0 && len(f.Documents) == 0 {
		ab.WriteString("(this run produced no artifact files, or its folder has been cleaned up)\n")
	}
	if len(f.Artifacts) == 0 && len(f.Documents) > 0 {
		ab.WriteString("(no files under the run's artifacts folder — this workflow's steps wrote to the " +
			"SDK outputs folder instead, and those documents are quoted in full above)\n")
	}
	for _, a := range f.Artifacts {
		fmt.Fprintf(&ab, "- %s\n", a)
	}
	if f.ArtifactsTruncated {
		ab.WriteString("(inventory truncated — more files exist than are listed)\n")
	}
	for _, doc := range f.Documents {
		fmt.Fprintf(&ab, "- %s (produced by the step %q; its text is above)\n", doc.Path, doc.StepTitle)
	}
	if len(f.Artifacts) > 0 {
		ab.WriteString("\nThe CONTENTS of the artifacts-folder files are not included. Judge what their " +
			"existence and names support, no more.\n")
	}
	ev.Sections = append(ev.Sections, Section{Heading: "ARTIFACTS THE RUN PRODUCED", Body: ab.String()})

	if len(f.Logs) > 0 {
		var lb strings.Builder
		lb.WriteString("The END of each node's working log — how the step was actually carried out, capped.\n")
		for _, l := range f.Logs {
			fmt.Fprintf(&lb, "\n--- %s (run %s) ---\n%s\n", l.NodeID, l.RunID, l.Tail)
		}
		ev.Sections = append(ev.Sections, Section{Heading: "HOW IT WAS DONE (capped log tails)", Body: lb.String()})
	}

	return ev
}

// sdkStepEvidence is one declared step's recorded evidence, with the name a
// reviewer can cite it by.
type sdkStepEvidence struct {
	Title string
	Body  string
}

// sdkRunEvidence classifies checks through the same frozen-request inspection
// used by journeys, and splits the result into a run-level preamble and one
// block per declared step. Checkpoint text is quoted evidence, not a fresh
// evaluation.
func sdkRunEvidence(run store.Run) (string, []sdkStepEvidence) {
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Workflow    string `json:"workflow"`
			Instruction string `json:"instruction"`
			Nodes       []struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Kind        string `json:"kind"`
				Instruction string `json:"instruction"`
			} `json:"nodes"`
		} `json:"request"`
	}
	var doc struct {
		SDK struct {
			Phase string `json:"phase"`
			Nodes []struct {
				ID       string   `json:"id"`
				Phase    string   `json:"phase"`
				Findings []string `json:"findings"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" || json.Unmarshal(run.Doc, &doc) != nil || doc.SDK.Phase == "" {
		return "No recorded SDK step evidence is available. Do not infer completion or verification.\n", nil
	}
	inspected := journey.InspectRun(run)
	checks := map[string]journey.CriterionStatus{}
	for _, check := range inspected.Criteria {
		checks[check.ID] = check
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "workflow: %s; recorded phase: %s\n", launch.Request.Workflow, doc.SDK.Phase)
	if store.IsRehearsal(run) {
		sb.WriteString("REHEARSAL: these records do not establish an executed delivery.\n")
	}
	if launch.Request.Instruction != "" {
		fmt.Fprintf(&sb, "Frozen instruction: %s\n", collapse(launch.Request.Instruction, 1200))
	}
	sb.WriteString("Completion alone is not verification. Model reviews are fallible; deterministic checks establish only their configured conditions.\n")
	states := map[string]int{}
	for i, node := range doc.SDK.Nodes {
		if _, duplicate := states[node.ID]; duplicate {
			return sb.String() + "Duplicate SDK checkpoint step identities; no step verification can be established.\n", nil
		}
		states[node.ID] = i
	}
	if inspected.FailureReason != "" {
		fmt.Fprintf(&sb, "Recorded run finding: %s\n", inspected.FailureReason)
	}

	var steps []sdkStepEvidence
	describe := func(id, title, phase, instruction string, findings []string) {
		var b strings.Builder
		fmt.Fprintf(&b, "- step %s (%s): phase=%s; ", id, collapse(title, 160), phase)
		if check, ok := checks[id]; ok {
			basis := "deterministic check"
			if check.Basis == "model_review" {
				basis = "model review (fallible)"
			}
			fmt.Fprintf(&b, "%s; check passed=%t\n", basis, check.Proven)
		} else {
			b.WriteString("no configured check; ordinary completion supplies no verification\n")
		}
		if instruction != "" {
			fmt.Fprintf(&b, "  Frozen instruction: %s\n", collapse(instruction, 800))
		}
		if len(findings) > 0 {
			fmt.Fprintf(&b, "  Recorded findings: %s\n", collapse(strings.Join(findings, "; "), 800))
		}
		name := strings.TrimSpace(title)
		if name == "" {
			name = id
		}
		steps = append(steps, sdkStepEvidence{Title: collapse(name, 160), Body: b.String()})
	}
	if launch.Request.Workflow == "node-workflow" {
		for i, node := range launch.Request.Nodes {
			if i >= maxRunEvidenceNodes {
				steps = append(steps, sdkStepEvidence{
					Title: "remaining steps",
					Body:  fmt.Sprintf("… and %d more declared steps, not listed here.\n", len(launch.Request.Nodes)-maxRunEvidenceNodes),
				})
				break
			}
			phase := "pending"
			var findings []string
			if at, ok := states[node.ID]; ok {
				phase = doc.SDK.Nodes[at].Phase
				findings = doc.SDK.Nodes[at].Findings
			}
			describe(node.ID, node.Title, phase, node.Instruction, findings)
		}
	} else {
		describe("delivery", launch.Request.Workflow, doc.SDK.Phase, "", nil)
	}
	return sb.String(), steps
}

// sdkRunSummary is the whole of that evidence as one block, for the supporting
// runs of a SKILL review, where each run is already its own heading.
func sdkRunSummary(run store.Run) string {
	preamble, steps := sdkRunEvidence(run)
	var sb strings.Builder
	sb.WriteString(preamble)
	for _, step := range steps {
		sb.WriteString(step.Body)
	}
	return sb.String()
}

// sdkRunSections is the same evidence for a RUN review, one section per step so
// that a citation names the step it came from.
func sdkRunSections(run store.Run) []Section {
	preamble, steps := sdkRunEvidence(run)
	out := []Section{{Heading: "SDK RUN COMPLETION AND CONFIGURED CHECKS", Body: preamble}}
	for _, step := range steps {
		out = append(out, Section{Heading: "STEP: " + step.Title, Body: step.Body})
	}
	return out
}

// ReadLogTails collects capped tails of explicitly named run logs. It lives
// here rather than in the API because it is evidence assembly, and it is
// best-effort by design: a cleaned-up run folder makes the evidence thinner,
// never the review impossible.
func ReadLogTails(runsDir string, nodes []NodeTail) []NodeTail {
	if runsDir == "" {
		return nil
	}
	out := make([]NodeTail, 0, len(nodes))
	for _, n := range nodes {
		if len(out) >= maxLogNodes {
			break
		}
		if !safeSegment(n.RunID) || !safeSegment(n.NodeID) {
			continue
		}
		tail := readTail(filepath.Join(runsDir, n.RunID, n.NodeID+".log"), maxLogTail)
		if tail == "" {
			continue
		}
		out = append(out, NodeTail{RunID: n.RunID, NodeID: n.NodeID, Tail: tail})
	}
	return out
}

// ListArtifacts names the files a run produced, sorted and bounded.
func ListArtifacts(runsDir, runID string) ([]string, bool) {
	if runsDir == "" || !safeSegment(runID) {
		return nil, false
	}
	entries, err := os.ReadDir(filepath.Join(runsDir, runID, "artifacts"))
	if err != nil {
		return nil, false
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) > maxArtifactsListed {
		return names[:maxArtifactsListed], true
	}
	return names, false
}

// ReadProducedDocuments reads the documents this run recorded, for the review
// prompt. The lookup and the bounds live in internal/rundocs, because the skill
// extractor reads exactly the same files (D-14) and two copies of "where a step
// writes" is how one of them came to be looking in the wrong folder.
func ReadProducedDocuments(runsDir string, run store.Run) []rundocs.Document {
	if runsDir == "" || !safeSegment(run.RunID) {
		return nil
	}
	return rundocs.Read(filepath.Join(runsDir, run.RunID), run)
}

// ---- small helpers --------------------------------------------------------

func safeSegment(s string) bool {
	return s != "" && s != "." && s != ".." &&
		!strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}

// readTail returns the last n bytes of a file, cut to a rune and then a line
// boundary. Any error yields "".
func readTail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	s := string(b)
	if len(s) > n {
		s = s[len(s)-n:]
		for len(s) > 0 && !isRuneStart(s[0]) {
			s = s[1:]
		}
		if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
			s = s[i+1:]
		}
	}
	return strings.TrimSpace(strings.ToValidUTF8(s, ""))
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func statusOf(sk store.Skill) string {
	if store.IsDraftSkill(sk) {
		return "DRAFT — in no prompt until a human approves it"
	}
	return "active"
}

func metaStrings(meta map[string]any, key string) []string {
	raw, ok := meta[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func metaEvidence(meta map[string]any) []knowledge.Evidence {
	raw, ok := meta["evidence"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []knowledge.Evidence
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "(this run)"
	}
	return s
}

// collapse squeezes whitespace and caps at n bytes on a rune boundary.
func collapse(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
