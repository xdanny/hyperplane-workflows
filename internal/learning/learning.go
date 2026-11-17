// Package learning proposes draft methods after a human accepts a completed SDK
// attempt. Each attempt can claim one automatic extraction; promotion remains manual.
package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// RecordType is the records-table bucket holding one row per run attempt the
// platform has tried to learn from. The row is a CLAIM first and a result
// second — see ClaimRecord.
const RecordType = "learning"

// Outcome statuses for Result.Status and the claim record's state.
const (
	StatusDrafted   = "drafted"   // a draft skill is waiting in the inbox
	StatusSupported = "supported" // this delivery backed a draft that already existed
	StatusDuplicate = "duplicate" // an ACTIVE skill already covers the method
	StatusSkipped   = "skipped"   // nothing here to learn from
	StatusFailed    = "failed"    // extraction could not run
)

// ErrAlreadyLearned marks a delivery whose learning claim is already taken.
// Re-firing would mean a second billed codex call for the same acceptance.
var ErrAlreadyLearned = errors.New("attempt has already been learned from")

// Config carries the paths and engine one extraction needs.
type Config struct {
	// RunsDir holds extraction logs in the selected run folder.
	RunsDir string
	// SchemasDir holds skill-extract.json.
	SchemasDir string
	// TargetRoot is the codex cwd.
	TargetRoot string
	// Codex overrides the engine. Nil ⇒ the real adapter. Tests inject a canned
	// proposal so the whole path runs without spawning codex.
	Codex knowledge.CodexFunc
	// Extract overrides the extraction entry point itself. Nil ⇒
	// knowledge.ExtractSkill. Present so a caller can exercise the hook without
	// an extraction at all.
	Extract func(ctx context.Context, st *store.Store, ex knowledge.Extractor, runID string) (knowledge.ExtractResult, error)
}

// Result is what one acceptance taught the platform.
type Result struct {
	JourneyKey string `json:"journeyKey"`
	DecisionID string `json:"decisionId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	Status     string `json:"status"`
	SkillID    string `json:"skillId,omitempty"`
	// SupportRuns is how many distinct runs now stand behind the skill this
	// acceptance landed on. One is an anecdote; the count is what the proposal
	// card shows in place of a self-reported confidence score.
	SupportRuns int    `json:"supportRuns,omitempty"`
	Message     string `json:"message"`
}

// FromAcceptedDelivery considers only the latest viewed attempt. A stored
// acceptance must cover that same run. INSERT-only claims prevent repeat calls,
// while an accepted continuation gets its own claim and can support a draft.
func FromAcceptedDelivery(ctx context.Context, st *store.Store, cfg Config, j journey.Journey) (Result, error) {
	res := Result{JourneyKey: j.Key}

	if len(j.Attempts) == 0 || j.Attempts[0].State != "passed" {
		res.Status, res.Message = StatusSkipped, "the current attempt did not complete successfully — no automatic extraction"
		return res, nil
	}
	runID := j.Attempts[0].RunID
	res.RunID = runID
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		return res, fmt.Errorf("learning: load current attempt: %w", err)
	}
	if run.State != "passed" || store.IsRehearsal(run) {
		res.Status = StatusFailed
		res.Message = "the current attempt must be passed and cannot be a rehearsal"
		return res, fmt.Errorf("%w: %s", knowledge.ErrRunNotEligible, res.Message)
	}
	decision, err := st.DeliveryDecisionForRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) || err == nil && decision.Decision != "accepted" {
		res.Status, res.Message = StatusSkipped, "the current attempt has no recorded acceptance — no automatic extraction"
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("learning: current attempt acceptance: %w", err)
	}
	// An acceptance the server recorded itself, under a workflow's sign-off
	// policy, is a check result restated — not a person saying the method was
	// worth keeping. Extraction learns from a PERSON's acceptance only, and the
	// rule is enforced on the stored record so no caller can route around it.
	if decision.DecidedBy == store.DecidedByAuto {
		res.Status, res.Message = StatusSkipped, "this delivery was accepted automatically by its workflow's sign-off policy, not by a person — no automatic extraction"
		return res, nil
	}
	res.DecisionID = decision.ID

	if err := st.ClaimRecord(ctx, store.Record{
		Type:  RecordType,
		ID:    runID,
		State: "claimed",
		RunID: runID,
		Kind:  "skill-extraction",
		Doc:   claimDoc(j, runID, decision.ID, "claimed", "", ""),
	}); err != nil {
		if errors.Is(err, store.ErrExists) {
			return res, ErrAlreadyLearned
		}
		return res, fmt.Errorf("learning: claim delivery %q: %w", j.Key, err)
	}

	out, err := extract(ctx, st, cfg, j, runID, decision.ID)
	if err != nil {
		// The claim is kept, not released. An acceptance happens once, so there
		// is no second trigger to retry from, and re-running on the next boot
		// would spend the engine again on a delivery nobody re-decided. The
		// failure is recorded with its reason, and the manual path
		// (POST /api/runs/{id}/extract-skill) is still there for a retry
		// somebody actually asks for.
		res.Status = StatusFailed
		res.Message = err.Error()
		finish(ctx, st, j, res)
		return res, fmt.Errorf("learning: extract from run %s: %w", runID, err)
	}

	res.Message = out.Message
	switch {
	case out.Status == knowledge.ResultDuplicate && out.SupportRuns > 0 && out.Skill != nil:
		// The method was already proposed and is still awaiting a verdict, so
		// this delivery joined its evidence rather than being discarded. That
		// is a different fact from "an approved skill already covers this", and
		// the record says so: the draft in the inbox now rests on k runs, which
		// is exactly what a reader weighing it needs to know.
		res.Status = StatusSupported
		res.SkillID = out.Skill.ID
		res.SupportRuns = out.SupportRuns
	case out.Status == knowledge.ResultDuplicate && out.Duplicate != nil:
		// A near-duplicate is not a question for a human — the base already
		// holds the method, and the answer is to refine that skill, not to
		// approve a second one. It goes to the journal, silently, so the trail
		// exists without an inbox card that has no useful verdict.
		res.Status = StatusDuplicate
		res.SkillID = out.Duplicate.SkillID
	case out.Skill != nil:
		res.Status = StatusDrafted
		res.SkillID = out.Skill.ID
		res.SupportRuns = out.SupportRuns
	default:
		res.Status = StatusSkipped
		if res.Message == "" {
			res.Message = "extraction proposed nothing"
		}
	}
	finish(ctx, st, j, res)
	return res, nil
}

// extract reads the accepted run's committed SDK evidence, with the delivery's
// provenance stamped onto whatever draft comes back.
func extract(ctx context.Context, st *store.Store, cfg Config, j journey.Journey, runID, decisionID string) (knowledge.ExtractResult, error) {
	run := cfg.Extract
	if run == nil {
		run = knowledge.ExtractSkill
	}
	ex := knowledge.Extractor{
		Codex:      cfg.Codex,
		SchemasDir: cfg.SchemasDir,
		TargetRoot: cfg.TargetRoot,
		Meta: map[string]any{
			"sourceJourney":  j.Key,
			"sourceDecision": decisionID,
			"sourceDelivery": firstNonEmpty(
				j.Name, j.Recipe, j.DAG, "delivery "+j.Key),
			"learnedFrom": "acceptance",
			"learnedAt":   time.Now().UTC().Format(time.RFC3339),
		},
	}
	if cfg.RunsDir != "" {
		ex.RunDir = filepath.Join(cfg.RunsDir, runID)
	}
	if j.Project != "" {
		ex.Meta["project"] = j.Project
	}
	return run(ctx, st, ex, runID)
}

// finish records the verdict on the claim record and in the run's journal.
// Both writes are best-effort: the learning already happened (or did not), and
// failing to file the paperwork must not turn a successful extraction into an
// error the caller reports.
func finish(ctx context.Context, st *store.Store, j journey.Journey, res Result) {
	_ = st.UpsertRecord(ctx, store.Record{
		Type:     RecordType,
		ID:       res.RunID,
		State:    res.Status,
		RunID:    res.RunID,
		Kind:     "skill-extraction",
		Consumed: true,
		Doc:      claimDoc(j, res.RunID, res.DecisionID, res.Status, res.SkillID, res.Message),
	})
	if res.RunID == "" {
		return
	}
	data, _ := json.Marshal(res)
	level := "info"
	if res.Status == StatusFailed {
		level = "warn"
	}
	_, _ = st.AppendEvent(ctx, store.Event{
		RunID: res.RunID,
		Level: level,
		Type:  "learning." + res.Status,
		Data:  data,
	})
}

// claimDoc is the record body: enough to read the row on its own.
func claimDoc(j journey.Journey, runID, decisionID, status, skillID, message string) store.JSON {
	b, err := json.Marshal(map[string]any{
		"journeyKey": j.Key,
		"decisionId": decisionID,
		"delivery":   firstNonEmpty(j.Name, j.Recipe, j.DAG),
		"project":    j.Project,
		"runId":      runID,
		"status":     status,
		"skillId":    skillID,
		"message":    message,
		"at":         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
