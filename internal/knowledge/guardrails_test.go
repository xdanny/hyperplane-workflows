// guardrails_test.go covers what a LEARNED skill must carry before it is
// allowed to shape a prompt: how many runs stand behind it, which lines in
// those runs say so, and a practice test it can be measured on.
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/rundocs"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// seedEvalset links a practice test to a skill, which is what ApproveSkill
// requires of anything the platform learned.
func seedEvalset(t *testing.T, st *store.Store, skillID string) {
	t.Helper()
	if err := st.UpsertEvalset(context.Background(), store.Evalset{
		ID:       "evalset-" + skillID,
		Name:     "Practice test for " + skillID,
		SkillRef: skillID,
		Cases: []store.EvalCase{{
			ID:       "case-1",
			Input:    json.RawMessage(`{"subject":"an unrelated subject"}`),
			Criteria: "applies the method to a subject the run never touched",
		}},
	}); err != nil {
		t.Fatalf("seed evalset for %q: %v", skillID, err)
	}
}

func TestPinEvidenceRequiresDisplayedCurrentCheckpointQuote(t *testing.T) {
	nodes := []sdkEvidenceNode{{ID: "gather", Checkpoint: "dropped two uncited claims"}, {ID: "review", Checkpoint: "every claim is cited"}}
	got := pinEvidence([]Evidence{
		{NodeID: "gather", Quote: " dropped two  uncited claims "},
		{RunID: "other-run", NodeID: "gather", Quote: "dropped two uncited claims"},
		{NodeID: "review", Quote: "invented favorable result"},
		{NodeID: "absent", Quote: "every claim is cited"},
		{NodeID: "review", Quote: "every claim is cited"},
		{NodeID: "gather", Quote: "dropped two uncited claims"},
	}, "run-current", nodes, nil)
	if len(got) != 2 || got[0].RunID != "run-current" || got[0].Quote != "dropped two uncited claims" || got[1].NodeID != "review" {
		t.Fatalf("incorrectly grounded excerpts: %+v", got)
	}
	if pinEvidence(nil, "run-current", nodes, nil) != nil {
		t.Fatal("empty evidence must remain empty")
	}
}

func TestPinEvidenceBoundsGroundedQuote(t *testing.T) {
	long := strings.Repeat("a", maxEvidenceQuote) + "—a long checkpoint finding"
	nodes := []sdkEvidenceNode{{ID: "gather", Checkpoint: long}}
	got := pinEvidence([]Evidence{{NodeID: "gather", Quote: long}}, "run-current", nodes, nil)
	if len(got) != 1 || len(got[0].Quote) > maxEvidenceQuote+len("…") || !strings.HasSuffix(got[0].Quote, "…") {
		t.Fatalf("unbounded or unmarked quote: %+v", got)
	}
}

// TestExtractSkillPinsEvidence proves the excerpts survive to the stored draft
// in the shape the UI reads: a quote with the run and node that produced it.
func TestExtractSkillPinsEvidence(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-evidence")
	skillID := uniqueID("evidence-method")
	seedPassedRun(t, st, runID, "passed")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	prop := sampleProposal(skillID, "Evidence method")
	prop["evidence"] = []map[string]any{
		{"runId": "", "nodeId": "verify", "quote": "every claim is cited"},
		{"runId": "", "nodeId": "gather", "quote": "three rivals, all subscription-priced"},
		{"runId": "", "nodeId": "hallucinated", "quote": "a node that never ran"},
	}

	res, err := ExtractSkill(ctx, st, Extractor{
		Codex: fakeCodex(t, prop), RunDir: t.TempDir(),
	}, runID)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Status != ResultDrafted {
		t.Fatalf("expected a draft, got %+v", res)
	}
	if res.SupportRuns != 1 {
		t.Errorf("a fresh draft rests on 1 run, got %d", res.SupportRuns)
	}

	stored, err := st.GetSkill(ctx, skillID)
	if err != nil {
		t.Fatalf("get stored skill: %v", err)
	}
	var meta struct {
		Evidence    []Evidence `json:"evidence"`
		SourceRuns  []string   `json:"sourceRuns"`
		SupportRuns int        `json:"supportRuns"`
	}
	if err := json.Unmarshal(stored.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if len(meta.Evidence) != 2 {
		t.Fatalf("want 2 pinned excerpts (the third names a node that never ran), got %+v", meta.Evidence)
	}
	for _, e := range meta.Evidence {
		if e.RunID != runID || e.NodeID == "" || e.Quote == "" {
			t.Errorf("excerpt is not openable: %+v", e)
		}
	}
	if len(meta.SourceRuns) != 1 || meta.SourceRuns[0] != runID || meta.SupportRuns != 1 {
		t.Errorf("support not recorded on the fresh draft: %+v", meta)
	}
}

// TestExtractSkillAccumulatesSupportOnDuplicateDraft is the anecdote fix: a
// second run that reaches the same method used to be discarded by the dedupe
// short-circuit, leaving every draft standing on exactly one run forever. It
// must now join the draft's support instead.
func TestExtractSkillAccumulatesSupportOnDuplicateDraft(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	firstRun := uniqueID("run-support-a")
	secondRun := uniqueID("run-support-b")
	seedPassedRun(t, st, firstRun, "passed")
	seedPassedRun(t, st, secondRun, "passed")

	skillID := uniqueID("supported-method")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	first := sampleProposal(skillID, "Supported method")
	first["evidence"] = []map[string]any{{"runId": "", "nodeId": "verify", "quote": "every claim is cited"}}
	if _, err := ExtractSkill(ctx, st, Extractor{
		Codex: fakeCodex(t, first), RunDir: t.TempDir(),
	}, firstRun); err != nil {
		t.Fatalf("first extraction: %v", err)
	}

	// A near-duplicate proposal from a DIFFERENT run: same method, new id.
	secondID := uniqueID("supported-methods")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), secondID) })
	second := sampleProposal(secondID, "Supported method")
	second["evidence"] = []map[string]any{{"runId": "", "nodeId": "gather", "quote": "three rivals, all subscription-priced"}}

	res, err := ExtractSkill(ctx, st, Extractor{
		Codex: fakeCodex(t, second), RunDir: t.TempDir(),
	}, secondRun)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if res.Status != ResultDuplicate || res.Duplicate == nil || res.Duplicate.SkillID != skillID {
		t.Fatalf("expected the second run to land on the first draft, got %+v", res)
	}
	if res.SupportRuns != 2 {
		t.Errorf("support count = %d, want 2", res.SupportRuns)
	}
	if _, err := st.GetSkill(ctx, secondID); !errors.Is(err, store.ErrNotFound) {
		t.Error("supporting a draft must not also create a second, near-identical skill")
	}

	stored, err := st.GetSkill(ctx, skillID)
	if err != nil {
		t.Fatalf("get draft after support: %v", err)
	}
	var meta struct {
		SourceRun   string     `json:"sourceRun"`
		SourceRuns  []string   `json:"sourceRuns"`
		SupportRuns int        `json:"supportRuns"`
		Evidence    []Evidence `json:"evidence"`
	}
	if err := json.Unmarshal(stored.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.SupportRuns != 2 || len(meta.SourceRuns) != 2 {
		t.Fatalf("draft should now rest on both runs, got %+v", meta)
	}
	if meta.SourceRuns[0] != firstRun || meta.SourceRuns[1] != secondRun {
		t.Errorf("source runs = %v, want [%s %s]", meta.SourceRuns, firstRun, secondRun)
	}
	if meta.SourceRun != firstRun {
		t.Errorf("sourceRun must keep naming the FIRST run for existing readers, got %q", meta.SourceRun)
	}
	if len(meta.Evidence) != 2 {
		t.Errorf("the supporting run's excerpt should be merged in, got %+v", meta.Evidence)
	}
	if !store.IsDraftSkill(stored) {
		t.Error("gaining support is not approval — the skill must still be a draft")
	}

	// Re-extracting the SAME run does not inflate the count: support is a set.
	repeat := sampleProposal(uniqueID("supported-method-again"), "Supported method")
	res2, err := ExtractSkill(ctx, st, Extractor{
		Codex: fakeCodex(t, repeat), RunDir: t.TempDir(),
	}, secondRun)
	if err != nil {
		t.Fatalf("repeat extraction: %v", err)
	}
	if res2.SupportRuns != 2 {
		t.Errorf("re-extracting the same run changed support to %d, want 2", res2.SupportRuns)
	}
}

// TestExtractSkillDoesNotRewriteActiveSkillSupport pins the other half: an
// ACTIVE skill is already in prompts and its support is a fact about what it
// was approved on, so a later run may not quietly edit it.
func TestExtractSkillDoesNotRewriteActiveSkillSupport(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-active-dupe")
	seedPassedRun(t, st, runID, "passed")

	activeID := uniqueID("active-method")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: activeID, Name: "Active method", Instruction: "Do it.",
		Metadata: json.RawMessage(`{"sourceRun":"run-old","sourceRuns":["run-old"],"supportRuns":1}`),
	}); err != nil {
		t.Fatalf("seed active skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), activeID) })

	res, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, sampleProposal(uniqueID("active-methods"), "Active method")),
		RunDir: t.TempDir(),
	}, runID)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Status != ResultDuplicate {
		t.Fatalf("expected a duplicate verdict, got %+v", res)
	}
	after, err := st.GetSkill(ctx, activeID)
	if err != nil {
		t.Fatalf("get active skill: %v", err)
	}
	if strings.Contains(string(after.Metadata), runID) {
		t.Errorf("an active skill's support was rewritten by a later run: %s", after.Metadata)
	}
}

// TestApproveLearnedDraftNeedsEvalset is the guardrail the audit asked for: a
// skill the platform proposed itself cannot enter prompts on nothing but runs
// that passed. It needs a practice test first.
func TestApproveLearnedDraftNeedsEvalset(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	id := uniqueID("learned-needs-eval")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: id, Name: "Learned method", Instruction: "Do the method.",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"run-a","sourceRuns":["run-a","run-b"],"supportRuns":2}`),
	}); err != nil {
		t.Fatalf("seed learned draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	_, err := ApproveSkill(ctx, st, id)
	if !errors.Is(err, ErrNoEvalset) {
		t.Fatalf("approve without an evalset: got %v, want ErrNoEvalset", err)
	}
	if !strings.Contains(err.Error(), "hyperplane eval gen "+id) {
		t.Errorf("the refusal must say how to fix it, got %q", err.Error())
	}
	still, err := st.GetSkill(ctx, id)
	if err != nil {
		t.Fatalf("get skill after refusal: %v", err)
	}
	if !store.IsDraftSkill(still) {
		t.Fatal("a refused approval must leave the skill a draft")
	}

	// With a practice test in place, the same approval goes through.
	seedEvalset(t, st, id)
	approved, err := ApproveSkill(ctx, st, id)
	if err != nil {
		t.Fatalf("approve with an evalset: %v", err)
	}
	if store.IsDraftSkill(approved) {
		t.Error("approve must clear the draft marker once an evalset exists")
	}
	if strings.Contains(string(approved.Metadata), "approvedWithoutEvalset") {
		t.Errorf("a properly evaluated approval must not be stamped as an override: %s", approved.Metadata)
	}
}

// TestApproveHandWrittenDraftIsExempt: the rule guards the AUTOMATED path. A
// person who wrote the instruction already supplied the judgement it stands in
// for, so their draft is not held behind an evalset.
func TestApproveHandWrittenDraftIsExempt(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	id := uniqueID("handwritten-draft")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: id, Name: "Hand-written method", Instruction: "I wrote this myself.",
		Metadata: json.RawMessage(`{"status":"draft"}`),
	}); err != nil {
		t.Fatalf("seed hand-written draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	approved, err := ApproveSkill(ctx, st, id)
	if err != nil {
		t.Fatalf("a hand-written draft needs no evalset: %v", err)
	}
	if store.IsDraftSkill(approved) {
		t.Error("approve must clear the draft marker")
	}
}

// TestApproveUnevaledOverride covers the deliberate escape hatch: it works, it
// is stamped on the skill, and it is written to the source run's journal — an
// override that leaves a trail rather than a silence.
func TestApproveUnevaledOverride(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	runID := uniqueID("run-override")
	seedPassedRun(t, st, runID, "passed")

	id := uniqueID("override-draft")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: id, Name: "Override method", Instruction: "Do the method.",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"` + runID + `"}`),
	}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	if _, err := ApproveSkill(ctx, st, id); !errors.Is(err, ErrNoEvalset) {
		t.Fatalf("without the override the approval must be refused, got %v", err)
	}

	t.Setenv(ApproveUnevaledEnv, "1")
	approved, err := ApproveSkill(ctx, st, id)
	if err != nil {
		t.Fatalf("approve with %s=1: %v", ApproveUnevaledEnv, err)
	}
	if store.IsDraftSkill(approved) {
		t.Error("the override must actually approve the skill")
	}
	var meta map[string]any
	if err := json.Unmarshal(approved.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta["approvedWithoutEvalset"] != true {
		t.Errorf("the override must be stamped on the skill, got %v", meta)
	}

	events, err := st.ListEvents(ctx, runID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Type == "learning.approved_without_evalset" {
			found = true
			if !strings.Contains(string(e.Data), id) {
				t.Errorf("the journal entry should name the skill, got %s", e.Data)
			}
		}
	}
	if !found {
		t.Errorf("the override must be journaled on the source run, got %d events", len(events))
	}
}

// The extractor is shown the delivered documents in full, and its best quote is
// usually in them. Pinning accepted checkpoint text only, so those quotes were
// dropped before storage and the draft card fell back to a status word (D-03).
// A quote that occurs in NOTHING it was shown is still refused.
func TestPinEvidenceAcceptsQuotesFromAnyDocumentItWasShown(t *testing.T) {
	nodes := []sdkEvidenceNode{{ID: "gather", Checkpoint: "phase: passed"}, {ID: "write", Checkpoint: "phase: passed"}}
	documents := []rundocs.Document{
		{StepID: "gather", StepTitle: "Gather", Path: "gather.md", Text: "Three sources disagreed on\nthe fee cap."},
		{StepID: "write", StepTitle: "Write", Path: "write.md", Text: "The brief states the fee cap as a range, not a number."},
	}
	got := pinEvidence([]Evidence{
		{NodeID: "write", Quote: "The brief states the fee cap as a range, not a number."},
		{NodeID: "gather", Quote: "Three sources disagreed on the fee cap."},
		{NodeID: "gather", Quote: "phase: passed"},
		{NodeID: "write", Quote: "the delivery proved the fee cap is correct"},
	}, "run-current", nodes, documents)
	if len(got) != 3 {
		t.Fatalf("delivered quotes dropped or invented: %+v", got)
	}
	// A document quote records WHICH file said it, so a reader can open it.
	if got[0].Source == nil || got[0].Source.StepID != "write" || got[0].Source.Path != "write.md" {
		t.Errorf("first quote lost its source: %+v", got[0].Source)
	}
	// Newlines in the file do not make a quote unfindable.
	if got[1].Source == nil || got[1].Source.Path != "gather.md" {
		t.Errorf("second quote lost its source: %+v", got[1].Source)
	}
	// A checkpoint quote names no document: the nodeId already says where it is.
	if got[2].Source != nil {
		t.Errorf("checkpoint quote claimed a document source: %+v", got[2].Source)
	}

	// A quote that appears in no shown text is refused, whatever step it names.
	if pinned := pinEvidence([]Evidence{{NodeID: "write", Quote: "an outcome nobody recorded"}}, "run-current", nodes, documents); len(pinned) != 0 {
		t.Errorf("stored an ungrounded quote: %+v", pinned)
	}
	// So is a quote attributed to a step this run never declared.
	if pinned := pinEvidence([]Evidence{{NodeID: "absent", Quote: "Three sources disagreed on the fee cap."}}, "run-current", nodes, documents); len(pinned) != 0 {
		t.Errorf("pinned evidence to an unlisted step: %+v", pinned)
	}
	// And a foreign run's excerpt stays refused.
	if pinned := pinEvidence([]Evidence{{RunID: "other-run", NodeID: "write", Quote: "The brief states the fee cap as a range, not a number."}}, "run-current", nodes, documents); len(pinned) != 0 {
		t.Errorf("pinned another run's excerpt: %+v", pinned)
	}
}
