package persona

// review.go — invoking a registered charter against a target.
//
// One codex call, read-only sandbox, hardened flags. The prompt is assembled
// out of the charter's OWN sections, so a persona invoked from the API is
// reading the same brief a persona spawned by hand reads, and two verdicts are
// comparable. What is added to the charter is exactly two things: the evidence
// the platform can hand a read-only agent, and the honest statement of what
// that agent cannot see.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const (
	// reviewTimeoutMs bounds one review call. A persona review is a careful read
	// of a lot of evidence, so it gets the same generous bound extraction has.
	reviewTimeoutMs = 6 * 60 * 1000
	// maxSectionBytes caps one evidence section, so a chatty run cannot crowd
	// the charter out of its own prompt. When it bites, the section says so.
	maxSectionBytes = 24 * 1024
	// SchemaFile is the codex --output-schema for a review.
	SchemaFile = "persona-review.json"
)

// Verdicts a review may return. The first three are the SKILL-REVIEW verdicts
// the charters already speak (APPROVE / REJECT / REVISE); the last three are
// the run-review verdicts, where "approve" would be a category error — nobody
// is approving a finished delivery, they are saying whether it holds up.
const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
	VerdictRevise  = "revise"
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictMixed   = "mixed"
)

var validVerdicts = map[string]bool{
	VerdictApprove: true, VerdictReject: true, VerdictRevise: true,
	VerdictPass: true, VerdictFail: true, VerdictMixed: true,
}

// CodexFunc is the engine entry point. It defaults to codex.Run; tests inject a
// canned review so the whole path runs without spawning a subprocess.
type CodexFunc func(ctx context.Context, o codex.Options) (codex.Result, error)

// Config carries the paths and engine one review needs.
type Config struct {
	SchemasDir string // holds persona-review.json
	TargetRoot string // codex cwd (read-only)
	RunDir     string // where the codex log + last-message sidecar land
	// Codex overrides the engine. Nil ⇒ the real adapter.
	Codex CodexFunc
}

// Options are the per-call knobs.
type Options struct {
	// Model overrides the engine model. It also selects the LOCAL engine: a
	// value of "local", "codex-local" or "codex-oss" (optionally
	// "local:<model>") runs codex's full agentic harness on a local open model
	// instead of the billed cloud one. Empty ⇒ the adapter's default.
	Model string
}

// Section is one labelled block of evidence in the review prompt.
type Section struct {
	Heading string
	Body    string
}

// Evidence is everything the platform hands the reviewer about one target.
type Evidence struct {
	Target Target
	// Title is what the target is called in human terms — the run's workflow, or
	// the skill's name. It goes in the prompt's header so the reviewer knows
	// what they are looking at before the first section.
	Title string
	// Note is the one honest sentence about what this evidence is NOT: what a
	// browser audit would have seen and this review cannot.
	Note     string
	Sections []Section
}

// Citation is one quote the reviewer pinned to a place in the evidence.
type Citation struct {
	// Ref is where the quote came from, in the terms the evidence used —
	// "node report-verify", "instruction", "evidence quote 2".
	Ref   string `json:"ref"`
	Quote string `json:"quote"`
}

// Review is one recorded verdict.
//
// It is ADVISORY, always, and the field says so rather than leaving it to a
// convention somebody could forget: no persona review gates an approval, and
// none of them replaces the evalset a learned draft needs (spec AI-12).
type Review struct {
	ID             string     `json:"id"`
	Persona        string     `json:"persona"`
	PersonaName    string     `json:"personaName,omitempty"`
	Bar            string     `json:"bar,omitempty"`
	CharterVersion string     `json:"charterVersion,omitempty"`
	Target         string     `json:"target"`
	TargetKind     string     `json:"targetKind"`
	TargetID       string     `json:"targetId"`
	TargetTitle    string     `json:"targetTitle,omitempty"`
	Model          string     `json:"model,omitempty"`
	Verdict        string     `json:"verdict"`
	Summary        string     `json:"summary"`
	Reasons        []string   `json:"reasons,omitempty"`
	Citations      []Citation `json:"citations,omitempty"`
	// RevisedWording is the exact replacement text a REVISE verdict owes the
	// human, so they can apply it without re-deriving it.
	RevisedWording string `json:"revisedWording,omitempty"`
	// Advisory is always true. It is serialised so a reader of the raw record
	// sees it too.
	Advisory   bool   `json:"advisory"`
	ReviewedAt string `json:"reviewedAt"`
}

// reviewOutput is codex's raw output, shaped by schemas/persona-review.json.
type reviewOutput struct {
	Verdict        string     `json:"verdict"`
	Summary        string     `json:"summary"`
	Reasons        []string   `json:"reasons"`
	Citations      []Citation `json:"citations"`
	RevisedWording string     `json:"revisedWording"`
}

// Run invokes one persona against one target's evidence and records the verdict.
//
// The record is INSERT-ONLY and several reviews of the same target are kept: a
// second persona's disagreement, or the same persona's re-read after a change,
// is precisely the evidence a human wants both halves of. Overwriting would
// destroy the thing this feature exists to accumulate.
func Run(ctx context.Context, st *store.Store, cfg Config, c Charter, ev Evidence, opts Options) (Review, error) {
	rev := Review{
		Persona:        c.ID,
		PersonaName:    c.Name,
		Bar:            c.Bar,
		CharterVersion: c.Version,
		Target:         ev.Target.String(),
		TargetKind:     ev.Target.Kind,
		TargetID:       ev.Target.ID,
		TargetTitle:    ev.Title,
		Advisory:       true,
	}

	if cfg.RunDir != "" {
		if err := os.MkdirAll(cfg.RunDir, 0o755); err != nil {
			return rev, fmt.Errorf("persona review: run dir %q: %w", cfg.RunDir, err)
		}
	}
	schemaPath := ""
	if cfg.SchemasDir != "" {
		schemaPath = filepath.Join(cfg.SchemasDir, SchemaFile)
	}

	runFn := cfg.Codex
	if runFn == nil {
		runFn = codex.Run
	}
	o := codex.Options{
		Instruction:      BuildPrompt(c, ev),
		OutputSchemaPath: schemaPath,
		Reasoning:        "high", // judging evidence against a professional bar is the hard part
		Cwd:              cfg.TargetRoot,
		RunDir:           cfg.RunDir,
		Label:            "persona-review-" + c.ID,
		// A reviewer READS. The sandbox stays fully locked down — same posture as
		// verify/gate/judge, and the reason a persona review can never change the
		// thing it is judging.
		SandboxMode: codex.SandboxReadOnly,
		TimeoutMs:   reviewTimeoutMs,
	}
	applyModel(&o, opts.Model)
	rev.Model = opts.Model

	cres, err := runFn(ctx, o)
	if err != nil {
		return rev, fmt.Errorf("persona review: codex: %w", err)
	}
	if cres.RateLimited {
		// Infrastructure, not a verdict. Saying "the persona rejected it" here
		// would be fabricated evidence in an evidence platform.
		return rev, fmt.Errorf("persona review: codex rate limited — retry later")
	}
	if !cres.OK || len(cres.JSON) == 0 {
		return rev, fmt.Errorf("persona review: codex returned no review (status %d)", cres.Status)
	}

	var out reviewOutput
	if err := json.Unmarshal(cres.JSON, &out); err != nil {
		return rev, fmt.Errorf("persona review: parse review: %w", err)
	}
	verdict := strings.ToLower(strings.TrimSpace(out.Verdict))
	if !validVerdicts[verdict] {
		return rev, fmt.Errorf("persona review: %q is not a verdict this platform records", out.Verdict)
	}
	rev.Verdict = verdict
	rev.Summary = strings.TrimSpace(out.Summary)
	rev.Reasons = out.Reasons
	rev.Citations = out.Citations
	rev.RevisedWording = strings.TrimSpace(out.RevisedWording)
	rev.ReviewedAt = time.Now().UTC().Format(time.RFC3339)

	if err := persist(ctx, st, &rev); err != nil {
		return rev, err
	}
	return rev, nil
}

// applyModel translates the review's model override onto the adapter options.
//
// "local" / "codex-local" / "codex-oss" select codex's LOCAL harness (same
// naming the worker pool uses), optionally with a model after a colon. Anything
// else is passed through as the model name.
func applyModel(o *codex.Options, model string) {
	m := strings.TrimSpace(model)
	if m == "" {
		return
	}
	name, rest, _ := strings.Cut(m, ":")
	switch strings.ToLower(name) {
	case "local", "codex-local", "codex-oss":
		o.OSS = true
		o.Model = strings.TrimSpace(rest)
	default:
		o.Model = m
	}
}

// persist writes the review as an insert-only record and, for a run target,
// journals it on the run so the verdict shows up in the run's own story.
//
// The id carries a sequence number so a second review of the same target by the
// same persona is a new row rather than an overwrite. The claim is retried on
// collision (two reviews racing), and gives up rather than falling back to an
// upsert — losing a verdict silently is exactly the failure this feature is
// meant to end.
func persist(ctx context.Context, st *store.Store, rev *Review) error {
	n, err := st.CountPersonaReviews(ctx, rev.Persona, rev.Target)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 8; attempt++ {
		rev.ID = ReviewID(rev.Persona, Target{Kind: rev.TargetKind, ID: rev.TargetID}, n+1+attempt)
		doc, merr := json.Marshal(rev)
		if merr != nil {
			return fmt.Errorf("persona review: encode record: %w", merr)
		}
		runID := ""
		if rev.TargetKind == TargetRun {
			runID = rev.TargetID
		}
		err = st.ClaimRecord(ctx, store.Record{
			Type:  store.PersonaReviewRecordType,
			ID:    rev.ID,
			State: rev.Verdict,
			RunID: runID,
			Kind:  "persona-review",
			// Consumed: a review is evidence, not a question. It must never queue
			// itself in the inbox as something waiting on a decision.
			Consumed: true,
			Doc:      doc,
		})
		if err == nil {
			journal(ctx, st, *rev)
			return nil
		}
		if !errors.Is(err, store.ErrExists) {
			return err
		}
	}
	return fmt.Errorf("persona review: could not claim a review id for %s on %s: %w", rev.Persona, rev.Target, err)
}

// ReviewID is the record id of the nth review of a target by a persona.
func ReviewID(personaID string, t Target, n int) string {
	return fmt.Sprintf("persona-review-%s-%s-%d", personaID, t.RecordKey(), n)
}

// journal writes the verdict into the target run's journal. Best-effort: the
// review is already recorded, and failing to file the paperwork must not turn a
// completed review into an error.
func journal(ctx context.Context, st *store.Store, rev Review) {
	if rev.TargetKind != TargetRun || rev.TargetID == "" {
		return
	}
	data, err := json.Marshal(rev)
	if err != nil {
		return
	}
	_, _ = st.AppendEvent(ctx, store.Event{
		RunID: rev.TargetID,
		Level: "info",
		Type:  "persona.review",
		Data:  data,
	})
}

// ParseReview decodes a stored review record body.
func ParseReview(doc store.JSON) (Review, error) {
	var rev Review
	if len(doc) == 0 {
		return rev, fmt.Errorf("persona review: empty record")
	}
	if err := json.Unmarshal(doc, &rev); err != nil {
		return rev, fmt.Errorf("persona review: decode record: %w", err)
	}
	return rev, nil
}

// ---- prompt assembly ------------------------------------------------------

// BuildPrompt assembles the review instruction.
//
// Order matters and mirrors how a persona is spawned by hand: who you are, what
// you are allowed to do, how you judge this kind of material, what you are
// looking at, and only then what to return. The charter's own text is used
// verbatim — this function adds framing, never opinions.
func BuildPrompt(c Charter, ev Evidence) string {
	var sb strings.Builder

	sb.WriteString("You are running as a REGISTERED AUDIT PERSONA inside Hyperplane. ")
	sb.WriteString("The charter below is your brief, verbatim from `")
	sb.WriteString(c.Source)
	sb.WriteString("`. Read it as yourself, not as a document about someone else.\n\n")

	writeSection(&sb, "=== IDENTITY ===", c.Section(SectionIdentity))
	writeSection(&sb, "=== RULES ===", c.Section(SectionRules))

	if ev.Target.Kind == TargetSkill {
		writeSection(&sb, "=== SKILL-REVIEW PROTOCOL ===", c.Section(SectionSkillReview))
	} else {
		sb.WriteString(`=== AUDIT PROTOCOL, APPLIED TO ONE RUN ===
Your audit protocol is written for a sweep of the whole platform. Here you are
reading ONE delivery. Apply the goals that the evidence below can actually
answer — custody, criteria, proof, what failed and what that failure means —
and skip the ones that need a browser or a fresh run. Name which goals you ran.

`)
		writeSection(&sb, "", c.Section(SectionAuditProto))
	}

	sb.WriteString(`=== WHAT THIS REVIEW IS, AND IS NOT ===
This is an IN-PLATFORM review. You have no browser, no shell, and no way to run
anything: the sandbox is read-only and the evidence below is everything the
platform can hand you. Where a rule of yours presumes the browser ("audit as a
user", "prefer the dry run"), apply its INTENT to this evidence and say plainly
what you could not check. A full UX audit is still run separately, by an agent
driving the app with your same charter.

Two rules hold absolutely, whatever your charter says about approving:

1. Your verdict is ADVISORY. It is recorded next to the measured result and
   shown to the human who decides. You are not pressing Approve, and nothing
   here is gated on what you say. A REJECT does not block; it is loud.
2. Never invent evidence. Every citation must be a VERBATIM quote from the
   material below, with a ref naming where it came from. A reader will check it
   against the source, so a quote that is not there is worse than no quote.
   If the evidence is too thin to judge, say so — "cannot judge from this" is a
   result worth having.

`)

	fmt.Fprintf(&sb, "=== THE MATERIAL — %s ===\n", ev.Target.String())
	if ev.Title != "" {
		fmt.Fprintf(&sb, "%s\n", ev.Title)
	}
	if ev.Note != "" {
		fmt.Fprintf(&sb, "\n%s\n", ev.Note)
	}
	for _, s := range ev.Sections {
		body, capped := capBody(s.Body)
		if strings.TrimSpace(body) == "" {
			body = "(nothing recorded)"
		}
		fmt.Fprintf(&sb, "\n--- %s ---\n%s\n", s.Heading, body)
		if capped {
			sb.WriteString("(this section was truncated — it is longer than the review prompt can carry)\n")
		}
	}

	sb.WriteString(`
=== RETURN ===
A single JSON object matching the schema, and nothing else:

- verdict: for a SKILL target one of "approve" | "reject" | "revise" — your
  charter's own three. For a RUN target one of "pass" | "fail" | "mixed":
  whether the delivery holds up against your bar. Use "mixed" when it does in
  part; it is not a hedge, it is the common honest answer.
- summary: one or two sentences in your own voice, leading with the verdict and
  the single fact that decided it.
- reasons: the findings, each self-contained and specific. Name what is wrong or
  what carried it, not how you feel about it.
- citations: the verbatim quotes your reasons rest on, each {ref, quote}. ref
  names the section or node the quote came from, copied from the headings above.
- revisedWording: on a "revise" verdict, the exact replacement text, written
  out in full so a human can apply it without re-deriving it. Empty otherwise.

`)
	if c.Bar != "" {
		fmt.Fprintf(&sb, "Your bar, in your own words: %q\n", c.Bar)
	}
	return sb.String()
}

func writeSection(sb *strings.Builder, heading, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	if heading != "" {
		sb.WriteString(heading)
		sb.WriteString("\n")
	}
	sb.WriteString(body)
	sb.WriteString("\n\n")
}

// capBody bounds one evidence section and reports whether the cap bit. The cut
// lands on a rune boundary: half a multibyte rune in a prompt is garbage, and
// the same text is persisted, where Postgres rejects invalid UTF-8 outright.
func capBody(s string) (string, bool) {
	if len(s) <= maxSectionBytes {
		return s, false
	}
	n := maxSectionBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}
