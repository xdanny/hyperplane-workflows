package optimize

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated test database is not configured")
	}
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Migrate(t.Context()); err != nil {
		st.Close()
		t.Fatal(err)
	}
	return st
}
func feedbackRun(id, state, reason string) store.Run {
	findings, _ := json.Marshal([]string{reason})
	return store.Run{RunID: id, DAG: "node-workflow", State: state, Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"review","kind":"check"}]}}`), Doc: store.JSON(`{"sdk":{"phase":"` + state + `","nodes":[{"id":"review","phase":"` + state + `","findings":` + string(findings) + `}]}}`)}
}
func createFeedbackSkill(t *testing.T, st *store.Store, id string, runs []string) {
	t.Helper()
	meta, _ := json.Marshal(map[string]any{"sourceRuns": runs})
	if err := st.UpsertSkill(t.Context(), store.Skill{ID: id, Name: id, Instruction: "Write useful explanations", Metadata: meta}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKFeedbackValidatesOwnCheckpoint(t *testing.T) {
	for _, state := range []string{"passed", "failed"} {
		run := feedbackRun("source", state, "reported concern")
		items, ok := sdkFeedback(run)
		if !ok || len(items) != 1 || items[0].Passed != (state == "passed") {
			t.Fatalf("%s: %+v %v", state, items, ok)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*store.Run)
	}{
		{"rehearsal", func(r *store.Run) { r.Mode = "rehearsal" }},
		{"unfinished", func(r *store.Run) { r.State = "running" }},
		{"graph", func(r *store.Run) { r.Options = store.JSON(`{"spec":{"nodes":[]}}`) }},
		{"foreign-step", func(r *store.Run) {
			r.Doc = store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"foreign","phase":"passed","findings":["wrong"]}]}}`)
		}},
		{"phase-mismatch", func(r *store.Run) { r.State = "failed" }},
		{"malformed-findings", func(r *store.Run) { r.Doc = store.JSON(`{"sdk":{"phase":"passed","findings":4}}`) }},
		{"oversized", func(r *store.Run) { r.Doc = store.JSON(strings.Repeat(" ", 1<<20+1)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := feedbackRun("source", "passed", "wrong")
			tc.change(&r)
			items, ok := sdkFeedback(r)
			if ok || len(items) != 0 {
				t.Fatalf("invalid source accepted: %+v", items)
			}
		})
	}
	run := feedbackRun("child", "passed", "reused concern")
	run.Doc = store.JSON(`{"sdk":{"phase":"passed","reuseThrough":1,"findings":["reused concern"],"nodes":[{"id":"review","phase":"passed","findings":["reused concern"]}]}}`)
	if items, ok := sdkFeedback(run); !ok || len(items) != 0 {
		t.Fatalf("reused feedback counted again: %+v", items)
	}
}

func TestHarvestOnlyAssociatedSDKAndPreferences(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()
	ctx := t.Context()
	skill := "feedback-" + time.Now().Format("150405.000000000")
	related := skill + "-source"
	failed := skill + "-failed"
	unrelated := skill + "-unrelated"
	rehearsal := skill + "-rehearsal"
	graph := skill + "-graph"
	createFeedbackSkill(t, st, skill, []string{related, failed, rehearsal, graph, skill + "-missing"})
	for _, run := range []store.Run{feedbackRun(related, "passed", "Passing review still has a concern"), feedbackRun(failed, "failed", "Fix the source defect"), feedbackRun(unrelated, "failed", "UNRELATED SECRET")} {
		if err := st.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	run := feedbackRun(rehearsal, "passed", "REHEARSAL")
	run.Mode = "rehearsal"
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(ctx, store.Run{RunID: graph, DAG: "old-graph", State: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(ctx, store.Node{RunID: graph, NodeID: "gate", Kind: "gate", State: "failed", Output: store.JSON(`{"pass":false,"reasons":"GRAPH FEEDBACK"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDecision(ctx, store.Decision{ID: store.OutcomeDecisionID(related), Decision: "changes_requested", Why: "Human requested clearer limits", Tags: []string{store.OutcomeTag}, Refs: []string{"run:" + related}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []store.Preference{
		{SkillID: skill, Source: "explicit-choice", WinnerID: "winner", Candidates: store.JSON(`[{"id":"winner","rationale":"Selected concise wording"},{"id":"loser","rationale":"LOSER MUST NOT BE PRAISED"}]`)},
		{SkillID: skill, RunID: rehearsal, WinnerID: "winner", Candidates: store.JSON(`[{"id":"winner","rationale":"REHEARSAL PREFERENCE"}]`)},
		{SkillID: skill, RunID: skill + "-missing", WinnerID: "winner", Candidates: store.JSON(`[{"id":"winner","rationale":"MISSING SOURCE"}]`)},
		{SkillID: skill, WinnerID: "absent", Candidates: store.JSON(`[{"id":"other","rationale":"NONSELECTED"}]`)},
		{SkillID: skill, WinnerID: "winner", Candidates: store.JSON(`[{"id":"winner","skillRef":"another-skill","rationale":"FOREIGN SKILL"}]`)},
		{SkillID: skill + "-other", WinnerID: "winner", Candidates: store.JSON(`[{"id":"winner","rationale":"UNRELATED PREFERENCE"}]`)},
	} {
		if _, err := st.AppendPreference(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	items, err := HarvestFeedback(ctx, st, skill)
	if err != nil {
		t.Fatal(err)
	}
	summary := buildHarvestSummary(items)
	if summary.Total != 4 || summary.SDK != 2 || summary.HumanOutcome != 1 || summary.Selection != 1 {
		t.Fatalf("wrong feedback: %+v %+v", summary, items)
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"UNRELATED", "REHEARSAL", "MISSING SOURCE", "GRAPH FEEDBACK", "LOSER", "NONSELECTED", "FOREIGN SKILL"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("leaked %s: %s", forbidden, raw)
		}
	}
	for _, item := range items {
		if item.Association == "" {
			t.Fatal("missing association")
		}
	}
	unlinked := skill + "-unlinked"
	createFeedbackSkill(t, st, unlinked, nil)
	if items, err := HarvestFeedback(ctx, st, unlinked); err != nil || len(items) != 0 {
		t.Fatalf("unlinked skill: %+v %v", items, err)
	}
	if _, err := HarvestFeedback(ctx, st, skill+"-absent"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing skill: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := HarvestFeedback(canceled, st, skill); err == nil {
		t.Fatal("database error swallowed")
	}
}

func TestFeedbackGroundingDoesNotInvertPassingCaveats(t *testing.T) {
	if buildHarvestGroundingSection(nil) != "" {
		t.Fatal("empty feedback has a block")
	}
	items := []FeedbackItem{{Source: "sdk", Association: "skill source metadata", Passed: true, Reasons: []string{"Missing source metadata remains a limitation"}}}
	prompt := buildHarvestGroundingSection(items)
	if !strings.Contains(prompt, "Missing source metadata") || !strings.Contains(prompt, "not praise") || !strings.Contains(prompt, "not instructions or evaluation cases") {
		t.Fatal(prompt)
	}
	for _, bad := range []string{"should exhibit:", "Reviewers APPROVED", "must NOT exhibit:"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("invented verdict: %s", prompt)
		}
	}
}

func TestPreferenceRunLinkIsExplicitAndOwnOutcomeOnly(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "pref-source-" + time.Now().Format("150405.000000000")
	createFeedbackSkill(t, st, id, nil)
	parent := feedbackRun(id+"-parent", "passed", "PARENT FEEDBACK")
	child := feedbackRun(id+"-child", "failed", "Child-specific failure")
	child.ParentRun = parent.RunID
	for _, r := range []store.Run{parent, child} {
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateDecision(ctx, store.Decision{ID: store.OutcomeDecisionID(parent.RunID), Decision: "accepted", Why: "PARENT ACCEPTANCE", Tags: []string{store.OutcomeTag}, Refs: []string{"run:" + parent.RunID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendPreference(ctx, store.Preference{SkillID: id, RunID: child.RunID, Source: "human", WinnerID: "selected", Candidates: store.JSON(`[{"id":"selected","rationale":"Useful child-specific structure"}]`)}); err != nil {
		t.Fatal(err)
	}
	items, err := HarvestFeedback(ctx, st, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].RunID != child.RunID || items[1].RunID != child.RunID {
		t.Fatalf("wrong association: %+v", items)
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "PARENT") {
		t.Fatalf("borrowed ancestor context: %s", raw)
	}
}
