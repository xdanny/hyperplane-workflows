package sdkruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A saved workflow can say that a run which passed every check it declared does
// not need a person to say so again. When it does, the acceptance is recorded
// here, at terminal persist, from the committed record — never from the
// worker's word for it.
//
// Three things keep this honest. It is labelled: the decision records
// `decidedBy: hyperplane:auto`, and every surface reads that as "accepted
// automatically", never as a person's signature. It is narrow: a run with NO
// declared checks is not auto-accepted, because "nothing failed" is not
// evidence, and neither is a run that failed, stopped, rehearsed, or passed
// only some of its checks. And it is inert for learning: skill extraction runs
// from a person's acceptance, so an automatic one teaches the platform nothing.
//
// Best-effort by design. If recording fails, the run simply stays queued for a
// person — the direction that asks for more human attention, not less.

// autoAcceptanceNote states the exact grounds, so a reader never has to guess
// what "automatically" was based on.
func autoAcceptanceNote(passed int) string {
	return fmt.Sprintf("Accepted automatically: all %d declared checks passed (workflow setting)", passed)
}

// recordAutomaticAcceptance records the outcome when the run's launch-time
// policy asks for it and its committed checkpoint earns it.
func recordAutomaticAcceptance(ctx context.Context, st *store.Store, runID string) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	run, err := st.GetRun(writeCtx, runID)
	if err != nil {
		return
	}
	if !autoAcceptable(run) {
		return
	}
	declared, passed := declaredChecks(run)
	now := time.Now().UTC()
	_, _ = st.CreateDecision(writeCtx, store.Decision{
		ID:     store.OutcomeDecisionID(runID),
		Title:  "Outcome · " + autoRunLabel(run),
		Status: "accepted",
		Context: fmt.Sprintf("Run %s (%s) passed %d/%d declared checks. The saved workflow accepts a run automatically when every declared check passes; no person reviewed this delivery.",
			run.RunID, run.DAG, passed, declared),
		Decision:  "accepted",
		Why:       autoAcceptanceNote(passed),
		Tags:      []string{store.OutcomeTag, "accepted", "automatic"},
		Refs:      []string{"run:" + runID},
		Project:   run.Project,
		Source:    "run:" + runID,
		DecidedBy: store.DecidedByAuto,
		DecidedAt: &now,
	})
}

// autoAcceptable reports whether this committed run meets every condition for
// an automatic acceptance.
func autoAcceptable(run store.Run) bool {
	if run.State != "passed" || store.RunSignOff(run) != store.SignOffAutoOnChecks {
		return false
	}
	// A rehearsal executed nothing. Both outcome endpoints refuse a human
	// acceptance on one; recording one here would walk around that refusal.
	if store.IsRehearsal(run) {
		return false
	}
	declared, passed := declaredChecks(run)
	return declared > 0 && passed == declared
}

// declaredChecks counts the checks this run declared and the ones its committed
// checkpoint proves passed. Only configured acceptance checks and independent
// model reviews count — the same tally every delivery surface reports.
func declaredChecks(run store.Run) (declared, passed int) {
	for _, criterion := range journey.InspectRun(run).Criteria {
		declared++
		if criterion.Proven {
			passed++
		}
	}
	return declared, passed
}

// autoRunLabel names the run the way the decision list will show it.
func autoRunLabel(run store.Run) string {
	if goal := strings.TrimSpace(run.Goal); goal != "" {
		return goal
	}
	return run.DAG
}
