// Package knowledge proposes draft methods from accepted SDK execution records.
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/rundocs"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// DuplicateThreshold is the trigram similarity above which a proposed skill is
// treated as a near-duplicate of one already in the base. Above it we refuse to
// create a second, overlapping skill and point at the refinement loop instead —
// a knowledge base full of near-duplicates is worse than one that grows slowly.
const DuplicateThreshold = 0.55

const (
	// maxOutputPreview caps how much of a single node's output enters the prompt.
	maxOutputPreview = 1200
	// maxPromptNodes caps how many nodes are described in the prompt.
	maxPromptNodes = 24
	// extractTimeoutMs bounds the extraction codex call.
	extractTimeoutMs = 6 * 60 * 1000

	// maxEvidence caps how many excerpts ONE extraction may pin onto a draft.
	// Evidence is the load-bearing part of a proposal, not a transcript: three
	// quotes a reader will actually read beat twenty they will not.
	maxEvidence = 3
	// maxEvidenceQuote caps a single pinned excerpt, in bytes.
	maxEvidenceQuote = 300
	// maxEvidenceStored caps the evidence a draft accumulates across all the
	// runs that support it, so a draft supported by ten runs stays readable.
	maxEvidenceStored = 6
)

// ApproveUnevaledEnv is the deliberate override for the evalset-before-approval
// rule. Set it to "1" to approve a learned draft that has no practice test yet;
// every use is stamped on the skill and written to the source run's journal, so
// the shortcut leaves a trail rather than a silence.
const ApproveUnevaledEnv = "HYPERPLANE_APPROVE_UNEVALED"

// Result statuses for ExtractResult.Status.
const (
	ResultDrafted   = "drafted"
	ResultDuplicate = "duplicate"
)

var (
	// ErrRunNotEligible marks a run that may not seed a skill: it did not pass,
	// has no coherent SDK checkpoint, or a requested check did not pass.
	ErrRunNotEligible = errors.New("run is not eligible for skill extraction")
	// ErrNotDraft marks an approve/reject call against a skill that is not a draft.
	ErrNotDraft = errors.New("skill is not a draft")
	// ErrNoEvalset marks an approve call against a LEARNED draft that has no
	// evalset yet. A skill the platform proposed itself is a hypothesis; letting
	// it into prompts on nothing but a passed run and a self-reported score is
	// how an anecdote becomes doctrine. The evalset is the practice test that
	// turns the hypothesis into something that can be measured — and, later,
	// re-measured by the refinement loop, which needs the same evalset anyway.
	//
	// Hand-written skills are exempt: a person who wrote the instruction has
	// already supplied the judgement this rule is standing in for.
	ErrNoEvalset = errors.New("learned skill has no evalset")
	// ErrRehearsalSource marks a learned draft whose every source run was a
	// REHEARSAL. Nothing was executed, so the "method that worked" names no
	// work; the draft stays where it is and is never approved into prompts.
	ErrRehearsalSource = errors.New("learned skill was extracted from a rehearsal")
)

// CodexFunc is the engine entry point used by extraction. It defaults to
// codex.Run; tests inject a canned result instead of spawning a subprocess.
type CodexFunc func(ctx context.Context, o codex.Options) (codex.Result, error)

// Extractor carries the engine and the paths one extraction needs. A zero
// Codex means "use the real adapter".
type Extractor struct {
	Codex      CodexFunc
	SchemasDir string // where schemas/skill-extract.json lives (platform root)
	TargetRoot string // codex cwd
	RunDir     string // where the codex log + last-message sidecars land
	// Meta is merged into the draft skill's metadata, letting the caller record
	// the provenance it knows and extraction does not (which delivery an
	// acceptance learned from, for instance). It can never overwrite the keys
	// extraction owns — status, sourceRun, sourceDag.
	Meta map[string]any
}

// SkillProposal is codex's output, shaped by schemas/skill-extract.json.
type SkillProposal struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Domain      string   `json:"domain"`
	Instruction string   `json:"instruction"`
	Doc         string   `json:"doc"`
	Tags        []string `json:"tags"`
	// Confidence is codex's own 0..1 score. It is persisted but deliberately
	// NOT shown on a card: a bare scalar with no explanation reads as rigour
	// the number does not have. Support count and pinned evidence are what a
	// reader is given instead.
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
	// Evidence is the handful of excerpts the extraction call itself picked as
	// the load-bearing ones — the lines in the run that made it name this
	// method. Pinned so the proposal can be checked against the run rather than
	// taken on trust.
	Evidence []Evidence `json:"evidence,omitempty"`
}

// Evidence is one pinned excerpt: which node of which run said it, and what it
// said. The pair (runId, nodeId) is exactly what the UI needs to open the run
// with that node selected, so a quote is one click from its source.
type Evidence struct {
	RunID  string `json:"runId,omitempty"`
	NodeID string `json:"nodeId"`
	Quote  string `json:"quote"`
	// Source names the DELIVERED DOCUMENT a quote was found in, when it was not
	// found in the step's committed checkpoint. The extractor is shown both;
	// evidence that could only be pinned to the checkpoint excerpt meant a
	// quote from the delivery itself was dropped before storage, leaving the
	// draft card quoting a status word instead of the work (D-03).
	//
	// Host-derived and never taken from the proposal: pinEvidence rebuilds every
	// record, so a model-supplied source is discarded.
	Source *EvidenceSource `json:"source,omitempty"`
}

// EvidenceSource is where a pinned quote was actually read.
type EvidenceSource struct {
	StepID string `json:"stepId"`
	// Path is relative to the run's outputs folder, as the checkpoint recorded it.
	Path string `json:"path"`
}

// DuplicateMatch reports an existing skill that already covers the method.
type DuplicateMatch struct {
	SkillID    string  `json:"skillId"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Similarity float64 `json:"similarity"`
	Advice     string  `json:"advice"`
}

// ExtractResult is the verdict of one extraction: either a draft skill was
// created, or an existing skill already covers the method.
type ExtractResult struct {
	RunID     string          `json:"runId"`
	Status    string          `json:"status"`
	Skill     *store.Skill    `json:"skill,omitempty"`
	Proposal  *SkillProposal  `json:"proposal,omitempty"`
	Duplicate *DuplicateMatch `json:"duplicate,omitempty"`
	// SupportRuns is how many distinct runs now stand behind the skill this
	// extraction landed on — 1 for a fresh draft, k for a draft this run
	// joined. It is the number that replaces the bare confidence scalar: "one
	// passed run" is an anecdote, and the card is expected to say so.
	SupportRuns int    `json:"supportRuns,omitempty"`
	Message     string `json:"message"`
}

// ExtractSkill proposes a reusable skill from a passed run and persists it as a
// draft. It returns a duplicate verdict (and persists nothing) when the base
// already holds a close match.
func ExtractSkill(ctx context.Context, st *store.Store, ex Extractor, runID string) (ExtractResult, error) {
	res := ExtractResult{RunID: runID}

	run, err := st.GetRun(ctx, runID)
	if err != nil {
		return res, fmt.Errorf("get run %q: %w", runID, err)
	}
	if run.State != "passed" {
		return res, fmt.Errorf("%w: run %s is %q, not passed", ErrRunNotEligible, runID, run.State)
	}
	if err := eligibleToLearnFrom(ctx, st, run); err != nil {
		return res, err
	}

	snapshot, err := inspectSDKExtraction(run)
	if err != nil {
		return res, err
	}

	runFn := ex.Codex
	if runFn == nil {
		runFn = codex.Run
	}
	if ex.RunDir != "" {
		// codex writes its log + last-message sidecar here; the dir may have been
		// cleaned up since the run finished.
		if err := os.MkdirAll(ex.RunDir, 0o755); err != nil {
			return res, fmt.Errorf("extract: run dir %q: %w", ex.RunDir, err)
		}
	}
	schemaPath := ""
	if ex.SchemasDir != "" {
		schemaPath = filepath.Join(ex.SchemasDir, "skill-extract.json")
	}
	// The files the run delivered, read from the folder its steps actually write
	// to (D-14). ex.RunDir is that run's folder; an empty one, or a cleaned-up
	// folder, simply yields nothing and the prompt says so.
	documents := rundocs.Read(ex.RunDir, run)
	cres, err := runFn(ctx, codex.Options{
		Instruction:      buildExtractPrompt(run, snapshot, documents),
		OutputSchemaPath: schemaPath,
		Reasoning:        "high", // naming the transferable method is the hard part
		Cwd:              ex.TargetRoot,
		RunDir:           ex.RunDir,
		Label:            "skill-extract",
		SandboxMode:      codex.SandboxReadOnly,
		TimeoutMs:        extractTimeoutMs,
	})
	if err != nil {
		return res, fmt.Errorf("extract: codex: %w", err)
	}
	if cres.RateLimited {
		// Infrastructure, not a verdict on the run — say so instead of reporting
		// "no skill proposed", which reads like the run had nothing to teach.
		return res, fmt.Errorf("extract: codex rate limited — retry later")
	}
	if !cres.OK || len(cres.JSON) == 0 {
		return res, fmt.Errorf("extract: codex proposed no skill (status %d)", cres.Status)
	}

	prop, err := parseProposal(cres.JSON)
	if err != nil {
		return res, err
	}
	// The extractor's own account of what it read, taken at its word. A
	// proposal whose rationale says the outputs were mocked has told us it
	// found no evidence; persisting it as a draft and stamping 0.9 on it is
	// how "the recorded outputs are sparse and mocked" ended up on an inbox
	// card as "learned from a passed run you accepted".
	if reason, standIn := standInRationale(prop.Rationale); standIn {
		return res, fmt.Errorf(
			"%w: the extraction's own rationale reports %s — a method named from stand-in output has no evidence behind it",
			ErrRunNotEligible, reason)
	}
	prop.Confidence = capThinConfidence(prop.Confidence, prop.Rationale)
	// Quotes must occur in something this extraction was actually shown: a
	// current-run checkpoint excerpt or one of the delivered documents.
	prop.Evidence = pinEvidence(prop.Evidence, run.RunID, snapshot.Nodes, documents)
	res.Proposal = &prop

	dup, err := findDuplicate(ctx, st, prop)
	if err != nil {
		// The dedupe probe is the only thing standing between a proposal and an
		// UpsertSkill over an existing skill. If it could not run, we do not know
		// whether this id is free — so we persist nothing.
		return res, err
	}
	if dup != nil {
		res.Status = ResultDuplicate
		res.Duplicate = dup
		res.Message = fmt.Sprintf(
			"%q (%.0f%% similar) already covers this method — %s",
			dup.SkillID, dup.Similarity*100, dup.Advice)

		// A second run reaching the same method is not noise to throw away: it
		// is the difference between an anecdote and a pattern. When the match is
		// a DRAFT (still nobody's decision, still out of every prompt), this run
		// joins its support instead of vanishing into a short-circuit.
		//
		// An ACTIVE skill is left alone — it is already in prompts, and the way
		// to act on new runs there is the refinement loop, which reads runs
		// directly.
		if dup.Status == store.SkillStatusDraft {
			sk, count, serr := recordSupport(ctx, st, dup.SkillID, run, prop.Evidence)
			if serr != nil {
				return res, serr
			}
			res.Skill = &sk
			res.SupportRuns = count
			res.Duplicate.Advice = supportAdvice(dup.SkillID, count)
			res.Message = fmt.Sprintf(
				"draft %q already covers this method — run %s recorded as supporting evidence (%s)",
				dup.SkillID, run.RunID, supportPhrase(count))
		}
		return res, nil
	}

	sk, err := persistDraft(ctx, st, run, prop, ex.Meta)
	if err != nil {
		return res, err
	}
	res.Status = ResultDrafted
	res.Skill = &sk
	res.SupportRuns = 1
	res.Message = fmt.Sprintf(
		"draft skill %q created from run %s (%s) — it stays out of prompts until approved: hyperplane skills approve %s",
		sk.ID, runID, supportPhrase(1), sk.ID)
	return res, nil
}

// eligibleToLearnFrom requires real execution and a human acceptance covering
// this attempt. SDK checks and human acceptance are different requirements.
func eligibleToLearnFrom(ctx context.Context, st *store.Store, run store.Run) error {
	if store.IsRehearsal(run) {
		return fmt.Errorf(
			"%w: run %s was a rehearsal (mode %q) — its outputs were stand-ins, so there is no real evidence to learn from",
			ErrRunNotEligible, run.RunID, run.Mode)
	}
	d, err := st.DeliveryDecisionForRun(ctx, run.RunID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf(
			"%w: nobody has recorded an outcome on run %s's delivery — a run that merely completed is not something a person judged worth keeping",
			ErrRunNotEligible, run.RunID)
	case err != nil:
		// The lookup is the only thing standing between a proposal and a card
		// that claims a human accepted this. Unknown must never read as "yes".
		return fmt.Errorf("extract: delivery decision for run %q: %w", run.RunID, err)
	case d.Decision != "accepted":
		return fmt.Errorf(
			"%w: run %s's delivery was decided %q, not accepted",
			ErrRunNotEligible, run.RunID, d.Decision)
	}
	return nil
}

// standInWords are the phrases an extraction uses when it is telling us the run
// it read did not really do anything. They are matched on the RATIONALE, which
// is the extractor's account of its own grounds.
var standInWords = []string{"mocked", "mock output", "stand-in", "standin", "simulated", "rehearsal", "dry run", "dry-run", "placeholder"}

// standInRationale reports whether the extraction admitted, in its own words,
// that what it read was not real output — and which word gave it away.
func standInRationale(rationale string) (string, bool) {
	lower := strings.ToLower(rationale)
	for _, w := range standInWords {
		if strings.Contains(lower, w) {
			return fmt.Sprintf("%q output", w), true
		}
	}
	return "", false
}

// thinWords mark a rationale that hedges on how much it had to go on. Unlike a
// stand-in admission this is not a lie in the making — the run was real — so the
// draft survives, with its self-reported score pulled down to what a hedge is
// worth.
var thinWords = []string{"sparse", "thin", "limited evidence", "little evidence", "few outputs", "incomplete"}

// thinConfidenceCeiling is what a hedged rationale may claim. The number is not
// shown on a card (support count and pinned evidence are), but it is persisted
// and read by anything that ranks drafts, so "the outputs are sparse … 0.9" must
// not survive the round trip.
const thinConfidenceCeiling = 0.3

// capThinConfidence lowers a self-reported score whose own rationale hedges.
func capThinConfidence(confidence float64, rationale string) float64 {
	lower := strings.ToLower(rationale)
	for _, w := range thinWords {
		if strings.Contains(lower, w) {
			return math.Min(confidence, thinConfidenceCeiling)
		}
	}
	return confidence
}

// supportPhrase says how much a draft rests on, in words that do not overstate
// it. One run is an anecdote and the sentence says so.
func supportPhrase(count int) string {
	if count <= 1 {
		return "extracted from a single run — treat it as an anecdote until another run supports it"
	}
	return fmt.Sprintf("extracted from %d runs", count)
}

// runCountPhrase counts runs without editorialising — the anecdote warning
// belongs where a proposal is being weighed, not in a refusal that is already
// making its own point.
func runCountPhrase(count int) string {
	if count == 1 {
		return "a single run"
	}
	return fmt.Sprintf("%d runs", count)
}

// supportAdvice replaces the "refine it instead" line when the match was a
// pending draft: nothing needs refining yet, the draft simply has one more run
// behind it and is still waiting for a verdict.
func supportAdvice(skillID string, count int) string {
	return fmt.Sprintf("that draft is still pending and now rests on %s: hyperplane skills approve %s",
		runCountPhrase(count), skillID)
}

// ApproveSkill flips a draft to active, clearing the draft marker so the skill
// becomes resolvable by workflow nodes. This is the human step the whole
// pipeline is built around.
//
// A LEARNED draft — one the platform proposed from runs of its own — is refused
// until an evalset names it (ErrNoEvalset). Approval is the moment a proposal
// starts shaping every prompt that resolves it, and the only thing behind an
// automatic proposal is a handful of runs that passed. The evalset is the
// practice test that makes the claim checkable, and the refinement loop needs
// exactly that evalset later anyway, so the requirement buys the skill its own
// improvement path as well as its entry ticket.
//
// A hand-written draft is exempt: the judgement this rule stands in for was
// already supplied by the person who wrote the instruction.
func ApproveSkill(ctx context.Context, st *store.Store, id string) (store.Skill, error) {
	sk, err := st.GetSkill(ctx, id)
	if err != nil {
		return sk, err
	}
	if !store.IsDraftSkill(sk) {
		return sk, fmt.Errorf("%w: skill %q is %q", ErrNotDraft, id, store.SkillStatus(sk))
	}
	meta := decodeMetadata(sk.Metadata)

	learnedFrom := supportingRuns(meta)
	overridden := false
	if len(learnedFrom) > 0 {
		// Extraction now refuses a rehearsal outright, so no NEW draft can be
		// standing on one. The drafts already in the base predate that rule and
		// must not be quietly approved into every prompt on the strength of a
		// run that executed nothing — and unlike a missing evalset this is not
		// a gap somebody can fill, so ApproveUnevaledEnv does not reach it.
		rehearsals, err := st.RehearsalRuns(ctx, learnedFrom)
		if err != nil {
			return sk, fmt.Errorf("approve skill %q: rehearsal lookup: %w", id, err)
		}
		if real := realRuns(learnedFrom, rehearsals); len(real) == 0 {
			return sk, fmt.Errorf(
				"%w: %q was learned from %s that executed nothing — its outputs were stand-ins, so there is no evidence to approve. "+
					"Run the delivery for real and let it be learned from again",
				ErrRehearsalSource, id, runCountPhrase(len(learnedFrom)))
		}
		has, err := hasEvalset(ctx, st, id)
		if err != nil {
			// Same discipline as the dedupe probe: "unknown" must not read as
			// "no evalset" (which would block a legitimate approval) and must
			// not read as "has one" (which would wave the guardrail through).
			return sk, fmt.Errorf("approve skill %q: evalset lookup: %w", id, err)
		}
		if !has {
			if os.Getenv(ApproveUnevaledEnv) != "1" {
				// Name the command that exists: `eval gen` stores an evalset
				// with skill_ref set to this id, which is exactly the row this
				// check looks for.
				return sk, fmt.Errorf(
					"%w: %q was learned from %s and cannot enter prompts until it has a practice test — "+
						"create one with `hyperplane eval gen %s`, then approve (or set %s=1 to override deliberately)",
					ErrNoEvalset, id, runCountPhrase(len(learnedFrom)), id, ApproveUnevaledEnv)
			}
			overridden = true
			// The override is recorded ON the skill, not just in a log line:
			// whoever reads this skill later should see that it entered prompts
			// without a test.
			meta["approvedWithoutEvalset"] = true
		}
	}

	meta["status"] = store.SkillStatusActive
	meta["approvedAt"] = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(meta)
	if err != nil {
		return sk, fmt.Errorf("approve skill %q: encode metadata: %w", id, err)
	}
	sk.Metadata = b
	if err := st.UpsertSkill(ctx, sk); err != nil {
		return sk, fmt.Errorf("approve skill %q: %w", id, err)
	}
	if overridden {
		journalUnevaledApproval(ctx, st, id, learnedFrom)
	}
	return st.GetSkill(ctx, id)
}

// SourceRuns returns every run a skill was learned from — the support list when
// there is one, else the single `sourceRun` a pre-support-counting draft carries.
// Empty for a hand-written skill, which is exactly how a caller tells "learned"
// from "authored".
func SourceRuns(sk store.Skill) []string { return supportingRuns(decodeMetadata(sk.Metadata)) }

// WithMetadataFlag returns a copy of the skill with one boolean stamped into its
// metadata. It is for facts a READER derives about a stored row rather than
// facts the row owns — a rehearsal provenance warning, say — so the API can hand
// a card what it needs to be honest without a migration and without a backfill
// that could drift from the column it was copied from.
//
// The skill is returned unchanged when the metadata cannot be re-encoded: a
// missing warning is a smaller failure than a mangled skill.
func WithMetadataFlag(sk store.Skill, key string, value bool) store.Skill {
	meta := decodeMetadata(sk.Metadata)
	meta[key] = value
	b, err := json.Marshal(meta)
	if err != nil {
		return sk
	}
	sk.Metadata = b
	return sk
}

// realRuns filters a draft's supporting runs down to the ones that actually
// executed. A run the store has never heard of counts as real: it may simply
// have been pruned, and treating an unknown as a rehearsal would retroactively
// condemn every draft whose run folder has aged out.
func realRuns(runs []string, rehearsal map[string]bool) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		if !rehearsal[r] {
			out = append(out, r)
		}
	}
	return out
}

// hasEvalset reports whether any evalset names this skill. It mirrors
// internal/optimize's findEvalsetBySkillRef — the same lookup the refinement
// loop uses to score a rewrite, which is precisely why the evalset is the right
// entry ticket: passing this check is what makes the skill improvable later.
func hasEvalset(ctx context.Context, st *store.Store, skillID string) (bool, error) {
	sets, err := st.ListEvalsets(ctx)
	if err != nil {
		return false, err
	}
	for _, es := range sets {
		if es.SkillRef == skillID {
			return true, nil
		}
	}
	return false, nil
}

// journalUnevaledApproval writes the override into the journal of every run
// that supported the draft, so the shortcut is visible from the runs it was
// taken on. Best-effort: the approval already happened, and a missing journal
// row must not turn it into an error. Events are keyed by run, so a source run
// that has since been deleted simply records nothing.
func journalUnevaledApproval(ctx context.Context, st *store.Store, skillID string, runs []string) {
	data, err := json.Marshal(map[string]any{
		"skillId":     skillID,
		"sourceRuns":  runs,
		"supportRuns": len(runs),
		"override":    ApproveUnevaledEnv + "=1",
		"at":          time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	for _, runID := range runs {
		_, _ = st.AppendEvent(ctx, store.Event{
			RunID: runID,
			Level: "warn",
			Type:  "learning.approved_without_evalset",
			Data:  data,
		})
	}
}

// RejectSkill deletes a draft. Only drafts may be rejected — an approved skill
// is deleted deliberately, not by declining a proposal.
func RejectSkill(ctx context.Context, st *store.Store, id string) error {
	sk, err := st.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	if !store.IsDraftSkill(sk) {
		return fmt.Errorf("%w: skill %q is %q", ErrNotDraft, id, store.SkillStatus(sk))
	}
	return st.DeleteSkill(ctx, id)
}

// ---- proposal handling ----------------------------------------------------

// parseProposal decodes and normalizes codex's output, rejecting a proposal
// that is missing the fields a skill cannot exist without.
func parseProposal(raw json.RawMessage) (SkillProposal, error) {
	var p SkillProposal
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("extract: malformed proposal JSON: %w", err)
	}
	normalizeProposal(&p)
	if p.ID == "" || p.Title == "" || strings.TrimSpace(p.Instruction) == "" {
		return p, fmt.Errorf("extract: proposal is missing id, title, or instruction")
	}
	return p, nil
}

// normalizeProposal slugifies the id, trims text fields, lowercases tags, and
// clamps confidence into 0..1.
func normalizeProposal(p *SkillProposal) {
	p.ID = strings.ToLower(slugify(strings.TrimSpace(p.ID)))
	p.Title = strings.TrimSpace(p.Title)
	p.Domain = strings.TrimSpace(p.Domain)
	p.Instruction = strings.TrimSpace(p.Instruction)
	p.Doc = strings.TrimSpace(p.Doc)
	p.Rationale = strings.TrimSpace(p.Rationale)

	tags := make([]string, 0, len(p.Tags))
	seen := map[string]bool{}
	for _, t := range p.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	p.Tags = tags

	p.Confidence = math.Min(1, math.Max(0, p.Confidence))
}

// regressionPolicyStamp reads the regression policy of the project a run belongs
// to, ready to stamp onto a draft.
//
// Best-effort: a project that cannot be read, or whose declarations will not
// parse, stamps nothing. The rendering itself lives in store.RegressionPolicyStamp
// so the rule has one home rather than one per caller.
func regressionPolicyStamp(ctx context.Context, st *store.Store, project string) map[string]string {
	project = strings.TrimSpace(project)
	if project == "" || st == nil {
		return nil
	}
	p, err := st.GetProject(ctx, project)
	if err != nil {
		return nil
	}
	decl, err := store.ParseDeclarations(p.Declarations)
	if err != nil {
		return nil
	}
	return store.RegressionPolicyStamp(decl, project)
}

// persistDraft writes the proposal into the skills table as a draft. The doc
// goes under metadata.doc for the knowledge reader and approved-skill tools.
//
// The write is INSERT-ONLY. findDuplicate already refuses a colliding id, but a
// draft must never be able to land on top of an existing skill even if that
// check were skipped, reordered, or raced by a concurrent extraction: an upsert
// here would replace an approved skill's instruction and stamp status:draft on
// it, quietly removing it from every prompt.
func persistDraft(ctx context.Context, st *store.Store, run store.Run, p SkillProposal, extra map[string]any) (store.Skill, error) {
	meta := map[string]any{}
	// Caller-supplied provenance goes in FIRST so the keys the lifecycle turns
	// on — status above all — are written by extraction and cannot be shadowed.
	for k, v := range extra {
		meta[k] = v
	}
	meta["status"] = store.SkillStatusDraft
	meta["sourceRun"] = run.RunID
	// sourceRuns/supportRuns are the honest version of "how much is behind
	// this": a list that grows when another run reaches the same method, and
	// its length. sourceRun stays as the FIRST run, which is what every
	// existing reader (inbox, lineage, provenance) already looks for.
	meta["sourceRuns"] = []string{run.RunID}
	meta["supportRuns"] = 1
	meta["sourceDag"] = run.DAG
	meta["confidence"] = p.Confidence
	meta["rationale"] = p.Rationale
	meta["extractedAt"] = time.Now().UTC().Format(time.RFC3339)
	if len(p.Evidence) > 0 {
		meta["evidence"] = p.Evidence
	}
	if len(p.Tags) > 0 {
		meta["tags"] = p.Tags
	}
	if p.Doc != "" {
		meta["doc"] = p.Doc
	}
	// What this draft would have to BEAT, from the project the run belongs to.
	//
	// D-2: a project could declare a regression policy — an evalset and a
	// margin — and no surface read it, so the Skill Lab said "skipped: no
	// evalset yet" about a project that had named one. Stamping the reference
	// here is deliberately only half the fix: nothing re-scores a draft against
	// that evalset yet, and the card that renders this says so in the same
	// sentence. A reference somebody can see is what makes the missing scoring
	// visible; a reference nobody can see is what made it invisible.
	if rp := regressionPolicyStamp(ctx, st, run.Project); rp != nil {
		meta["regressionPolicy"] = rp
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return store.Skill{}, fmt.Errorf("extract: encode metadata: %w", err)
	}

	sk := store.Skill{
		ID:   p.ID,
		Name: p.Title,
		// The description feeds the trigram search column and the UI card, so it
		// must describe the METHOD — the opening of the instruction is exactly
		// that, where the rationale is about this particular run.
		Description: firstSentence(p.Instruction, 220),
		Category:    p.Domain,
		Instruction: p.Instruction,
		Metadata:    b,
	}
	if err := st.InsertSkill(ctx, sk); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.Skill{}, fmt.Errorf(
				"extract: skill %q already exists — refusing to overwrite it with a draft: %w", sk.ID, err)
		}
		return store.Skill{}, fmt.Errorf("extract: persist draft %q: %w", sk.ID, err)
	}
	return st.GetSkill(ctx, sk.ID)
}

// ---- support + evidence ---------------------------------------------------

// recordSupport adds a run to the set of runs behind an existing DRAFT and
// merges in whatever evidence this extraction pinned. It returns the refreshed
// skill and the new support count.
//
// This is what a near-duplicate is FOR. The dedupe check used to end the story
// — a second run reaching the same method was discarded as noise — which left
// every draft standing on exactly one run forever, i.e. on an anecdote. Two
// runs that independently demonstrate the same method is the strongest signal
// extraction can produce, so it is recorded rather than thrown away.
//
// Re-extracting the SAME run is a no-op: the run set is a set.
func recordSupport(ctx context.Context, st *store.Store, skillID string, run store.Run, ev []Evidence) (store.Skill, int, error) {
	sk, err := st.GetSkill(ctx, skillID)
	if err != nil {
		return store.Skill{}, 0, fmt.Errorf("extract: load draft %q to record support: %w", skillID, err)
	}
	meta := decodeMetadata(sk.Metadata)
	if !store.IsDraftSkill(sk) {
		// Raced with an approval between the dedupe probe and here. An active
		// skill's support is a fact about what it was approved on; do not
		// rewrite it behind the approver's back.
		return sk, len(supportingRuns(meta)), nil
	}

	runs := supportingRuns(meta)
	changed := false
	if run.RunID != "" && !containsString(runs, run.RunID) {
		runs = append(runs, run.RunID)
		changed = true
	}
	evidence, merged := mergeEvidence(metaEvidence(meta), ev)
	changed = changed || merged
	if !changed {
		return sk, len(runs), nil
	}

	meta["sourceRuns"] = runs
	meta["supportRuns"] = len(runs)
	if len(evidence) > 0 {
		meta["evidence"] = evidence
	}
	meta["supportedAt"] = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(meta)
	if err != nil {
		return sk, len(runs), fmt.Errorf("extract: encode support metadata for %q: %w", skillID, err)
	}
	sk.Metadata = b
	if err := st.UpsertSkill(ctx, sk); err != nil {
		return sk, len(runs), fmt.Errorf("extract: record support on %q: %w", skillID, err)
	}
	updated, err := st.GetSkill(ctx, skillID)
	if err != nil {
		return sk, len(runs), fmt.Errorf("extract: reload %q after recording support: %w", skillID, err)
	}
	return updated, len(runs), nil
}

// supportingRuns returns the runs a skill was learned from, newest last. It
// reads the sourceRuns list and falls back to the single sourceRun key, so a
// draft written before support counting existed still reports honestly.
//
// An empty result means "not learned from a run at all" — a hand-written skill
// — which is the exemption ApproveSkill turns on.
func supportingRuns(meta map[string]any) []string {
	var out []string
	if raw, ok := meta["sourceRuns"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" && !containsString(out, s) {
				out = append(out, s)
			}
		}
	}
	if s, ok := meta["sourceRun"].(string); ok && s != "" && !containsString(out, s) {
		out = append(out, s)
	}
	return out
}

// metaEvidence decodes the pinned excerpts already on a skill.
func metaEvidence(meta map[string]any) []Evidence {
	raw, ok := meta["evidence"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []Evidence
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// mergeEvidence appends the excerpts that are genuinely new, capped at
// maxEvidenceStored. It reports whether anything was added.
func mergeEvidence(existing, incoming []Evidence) ([]Evidence, bool) {
	seen := map[string]bool{}
	for _, e := range existing {
		seen[evidenceKey(e)] = true
	}
	added := false
	out := existing
	for _, e := range incoming {
		if len(out) >= maxEvidenceStored {
			break
		}
		k := evidenceKey(e)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
		added = true
	}
	return out, added
}

func evidenceKey(e Evidence) string {
	return e.RunID + "\x00" + e.NodeID + "\x00" + e.Quote
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pinEvidence binds excerpts to what the extraction was SHOWN: the current
// run's checkpoint excerpts and the delivered documents supplied alongside
// them. It used to accept checkpoint text only, so a quote from the delivery —
// the text the prompt now prints in full — was dropped before storage, and the
// draft card was left quoting a status word (D-03).
//
// The refusal is unchanged in kind: the quote must occur somewhere the
// extraction actually read, and a quote that occurs nowhere is discarded rather
// than stored on a nodeId's authority. A document match records where it was
// read, because "which file said this" is what a reader checks next.
func pinEvidence(in []Evidence, runID string, nodes []sdkEvidenceNode, documents []rundocs.Document) []Evidence {
	byID := map[string]string{}
	for _, n := range nodes {
		byID[n.ID] = compact(n.Checkpoint)
	}
	type shownDocument struct{ stepID, path, text string }
	shown := make([]shownDocument, 0, len(documents))
	for _, doc := range documents {
		shown = append(shown, shownDocument{stepID: doc.StepID, path: doc.Path, text: compact(doc.Text)})
	}
	var out []Evidence
	seen := map[string]bool{}
	for _, e := range in {
		if len(out) >= maxEvidence {
			break
		}
		id, quote := strings.TrimSpace(e.NodeID), compact(e.Quote)
		if e.RunID != "" && e.RunID != runID || quote == "" {
			continue
		}
		// The step must be one of the listed ones, whichever text the quote
		// came from: evidence points at a step a reader can open.
		checkpoint, listed := byID[id]
		if !listed {
			continue
		}
		var source *EvidenceSource
		if !strings.Contains(checkpoint, quote) {
			// Prefer this step's own document, then any document shown.
			var match *shownDocument
			for i := range shown {
				if !strings.Contains(shown[i].text, quote) {
					continue
				}
				if shown[i].stepID == id {
					match = &shown[i]
					break
				}
				if match == nil {
					match = &shown[i]
				}
			}
			if match == nil {
				continue
			}
			source = &EvidenceSource{StepID: match.stepID, Path: match.path}
		}
		quote = truncate(quote, maxEvidenceQuote)
		pinned := Evidence{RunID: runID, NodeID: id, Quote: quote, Source: source}
		key := evidenceKey(pinned)
		if !seen[key] {
			out = append(out, pinned)
			seen[key] = true
		}
	}
	return out
}

// ---- dedupe ---------------------------------------------------------------

// findDuplicate looks for an existing skill that already covers the proposed
// method: an exact id collision, or a trigram match at or above
// DuplicateThreshold on either the id or the display name. Returns (nil, nil)
// when the method is genuinely new.
//
// Every store error is returned, never swallowed. This function is a SAFETY
// check, not a nicety: "no duplicate found" is what authorises the write, so a
// transient DB failure must read as "unknown" (abort) and never as "the id is
// free" — otherwise persistDraft's write would land on top of an existing,
// approved skill and stamp it back down to draft.
func findDuplicate(ctx context.Context, st *store.Store, p SkillProposal) (*DuplicateMatch, error) {
	// An id collision is a duplicate by definition — and persisting would
	// overwrite the existing skill if the write path were an upsert.
	existing, err := st.GetSkill(ctx, p.ID)
	switch {
	case err == nil:
		return newDuplicateMatch(existing, 1), nil
	case !errors.Is(err, store.ErrNotFound):
		return nil, fmt.Errorf("extract: dedupe probe for skill id %q: %w", p.ID, err)
	}

	var best *DuplicateMatch
	seen := map[string]bool{}
	queries := []string{p.Title, p.Domain, strings.Join(p.Tags, " ")}
	for _, q := range queries {
		if strings.TrimSpace(q) == "" {
			continue
		}
		hits, err := st.SearchSkills(ctx, q, 10)
		if err != nil {
			return nil, fmt.Errorf("extract: dedupe search %q: %w", q, err)
		}
		for _, h := range hits {
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			score := math.Max(
				trigramSimilarity(p.ID, h.ID),
				trigramSimilarity(p.Title, h.Name),
			)
			if score >= DuplicateThreshold && (best == nil || score > best.Similarity) {
				best = newDuplicateMatch(h, score)
			}
		}
	}
	return best, nil
}

func newDuplicateMatch(sk store.Skill, score float64) *DuplicateMatch {
	advice := fmt.Sprintf("refine it instead: hyperplane optimize %s --from-runs", sk.ID)
	if store.IsDraftSkill(sk) {
		advice = fmt.Sprintf("that draft is still pending: hyperplane skills approve %s", sk.ID)
	}
	return &DuplicateMatch{
		SkillID:    sk.ID,
		Name:       sk.Name,
		Status:     store.SkillStatus(sk),
		Similarity: score,
		Advice:     advice,
	}
}

// trigramSimilarity mirrors Postgres pg_trgm's similarity(): the Jaccard
// coefficient over both strings' trigram sets. Computing it in Go keeps the
// dedupe threshold explicit and unit-testable, while the store's trgm index
// still does the cheap candidate retrieval.
func trigramSimilarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for t := range ta {
		if _, ok := tb[t]; ok {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// trigrams lowercases the string, splits it on non-alphanumerics, and returns
// the set of 3-grams of each padded word (pg_trgm's word padding).
func trigrams(s string) map[string]struct{} {
	out := map[string]struct{}{}
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		padded := "  " + w + " "
		for i := 0; i+3 <= len(padded); i++ {
			out[padded[i:i+3]] = struct{}{}
		}
	}
	return out
}

// compact collapses whitespace so a node's output stays readable in the prompt.
func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate caps s at n BYTES, cutting only on a rune boundary. A naive s[:n]
// splits a multibyte rune — an em dash or a smart quote in a node's output is
// enough — and the resulting invalid UTF-8 is rejected by Postgres when the
// description is persisted, i.e. AFTER the billed codex call has already run.
// The same text also feeds the prompt, where a half-rune is simply garbage.
func truncate(s string, n int) string {
	if n <= 0 {
		return "…"
	}
	if len(s) <= n {
		return s
	}
	// s[n] is the first excluded byte; if it continues a rune, that rune
	// straddles the cut, so walk back to where it starts.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// firstSentence returns the leading sentence of s, capped at n characters.
func firstSentence(s string, n int) string {
	s = compact(s)
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	return truncate(s, n)
}

// decodeMetadata returns a skill's metadata as a mutable map, so a status flip
// preserves every other key (doc, tags, sourceRun, …).
func decodeMetadata(raw store.JSON) map[string]any {
	meta := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &meta)
	}
	return meta
}
