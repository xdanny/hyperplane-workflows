package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Collect describes one run's committed SDK checkpoint and its recorded human outcome.
// Continuation siblings and unverified artifact contents never supply evidence.
func Collect(ctx context.Context, st *store.Store, runID, kind string) (StateDoc, error) {
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		return StateDoc{}, fmt.Errorf("brief: load run %q: %w", runID, err)
	}
	doc := StateDoc{Kind: kind, SubjectID: runID, Project: run.Project, RunID: run.RunID,
		DAG: run.DAG, Goal: strings.TrimSpace(run.Goal), State: run.State,
		Rehearsal: isRehearsal(run), StartedAt: run.StartedAt, EndedAt: run.EndedAt}
	rd := parseRunDoc(run.Doc)
	doc.Recipe, doc.Slots = rd.Recipe, slotList(rd.Slots)
	if doc.Recipe != "" {
		if tmpl, err := st.GetTemplate(ctx, doc.Recipe); err == nil {
			doc.RecipePurpose = strings.TrimSpace(tmpl.Description)
		}
		if namer, err := st.NewRecipeNamer(ctx); err == nil {
			doc.RecipeName = namer.Name(doc.Recipe)
		}
	}
	evidence := journey.InspectRun(run)
	doc.ContinuedFrom = evidence.ContinuedFrom
	doc.Phase = evidence.Phase
	if run.State == "failed" || run.State == "blocked" || run.State == "interrupted" {
		doc.FailingNode = evidence.CurrentNode
		if evidence.CurrentNode != "" || evidence.FailureReason != "" {
			verdict, truncated := capText(evidence.FailureReason)
			doc.Failures = []Failure{{NodeID: evidence.CurrentNode, State: run.State, Verdict: verdict, VerdictTruncated: truncated}}
		}
	}
	for _, c := range evidence.Criteria {
		doc.Criteria = append(doc.Criteria, Criterion{ID: c.ID, Title: c.Title, Basis: c.Basis,
			ProofNode: c.ProofNode, ProofState: c.ProofState, Proven: c.Proven, Rehearsal: c.Rehearsal})
	}
	doc.Notes = append(doc.Notes, "This brief uses this run's committed SDK checkpoint. Artifact contents are not independently inspected or summarized; no scorecard claims are inferred from files or other runs.")
	if d, err := st.DeliveryDecisionForRun(ctx, runID); err == nil {
		doc.Delivery = DeliveryFrom(d)
	} else if !errors.Is(err, store.ErrNotFound) {
		doc.Notes = append(doc.Notes, "The delivery decision could not be read, so this document cannot say whether one exists.")
	}
	return doc, nil
}

func DeliveryFrom(d store.Decision) *Delivery {
	if d.ID == "" {
		return nil
	}
	at := d.CreatedAt
	if d.DecidedAt != nil {
		at = *d.DecidedAt
	}
	return &Delivery{DecisionID: d.ID, Verdict: d.Decision, Note: d.Why, DecidedAt: at}
}

type runDocFields struct {
	Recipe string            `json:"recipe"`
	Slots  map[string]string `json:"slots"`
}

func parseRunDoc(doc store.JSON) runDocFields {
	var d runDocFields
	if len(doc) > 0 {
		_ = json.Unmarshal(doc, &d)
	}
	return d
}

func slotList(m map[string]string) []Slot {
	out := make([]Slot, 0, len(m))
	for _, k := range sortedKeys(m) {
		if strings.TrimSpace(m[k]) == "" {
			continue
		}
		out = append(out, Slot{Name: k, Value: m[k]})
	}
	return out
}

func isRehearsal(run store.Run) bool { return store.IsRehearsal(run) }
