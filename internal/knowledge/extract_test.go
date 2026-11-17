package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ---------------------------------------------------------------------------
// Pure unit tests — no DB, no codex
// ---------------------------------------------------------------------------

func TestTrigramSimilarity(t *testing.T) {
	cases := []struct {
		name         string
		a, b         string
		wantAtLeast  float64
		wantLessThan float64
	}{
		{"identical", "competitive teardown", "competitive teardown", 0.99, 1.01},
		{"near-duplicate wording", "competitive teardown", "competitive-teardown", 0.9, 1.01},
		{"same method, reordered", "api contract review", "contract api review", 0.9, 1.01},
		{"unrelated", "competitive teardown", "postgres index tuning", 0, 0.2},
		{"empty", "", "anything", 0, 0.001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trigramSimilarity(tc.a, tc.b)
			if got < tc.wantAtLeast || got >= tc.wantLessThan {
				t.Errorf("similarity(%q,%q) = %.3f, want in [%.3f,%.3f)",
					tc.a, tc.b, got, tc.wantAtLeast, tc.wantLessThan)
			}
		})
	}
}

func TestNormalizeProposal(t *testing.T) {
	p := SkillProposal{
		ID:          "  Competitive Teardown!! ",
		Title:       "  Competitive teardown  ",
		Domain:      " research ",
		Instruction: "  Compare rivals feature by feature.  ",
		Tags:        []string{"Research", "research", "  ", "Teardown"},
		Confidence:  1.7,
	}
	normalizeProposal(&p)

	if p.ID != "competitive-teardown" {
		t.Errorf("id = %q, want %q", p.ID, "competitive-teardown")
	}
	if p.Title != "Competitive teardown" || p.Domain != "research" {
		t.Errorf("title/domain not trimmed: %q / %q", p.Title, p.Domain)
	}
	if p.Confidence != 1 {
		t.Errorf("confidence = %v, want clamped to 1", p.Confidence)
	}
	if len(p.Tags) != 2 || p.Tags[0] != "research" || p.Tags[1] != "teardown" {
		t.Errorf("tags = %v, want deduped+lowercased [research teardown]", p.Tags)
	}
}

// TestTruncateIsRuneSafe pins the property that matters downstream: the result
// is always valid UTF-8. A byte-slicing truncate cuts a multibyte rune in half,
// and the invalid string is only rejected later, by Postgres, on persist —
// after the billed codex call.
func TestTruncateIsRuneSafe(t *testing.T) {
	// "ab" + em dash (U+2014, 3 bytes) + "cd": a cut at 3, 4 or 5 bytes lands
	// inside the em dash.
	s := "ab—cd"
	for n := 1; n <= len(s)+2; n++ {
		got := truncate(s, n)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q, which is not valid UTF-8", s, n, got)
		}
		if strings.Contains(got, "�") {
			t.Errorf("truncate(%q, %d) = %q, contains a replacement char", s, n, got)
		}
	}
	// A cut straddling the em dash drops it whole rather than half of it.
	if got := truncate(s, 4); got != "ab…" {
		t.Errorf("truncate(%q, 4) = %q, want %q", s, got, "ab…")
	}
	// A cut on a boundary keeps everything up to it.
	if got := truncate(s, 5); got != "ab—…" {
		t.Errorf("truncate(%q, 5) = %q, want %q", s, got, "ab—…")
	}
	// Short enough to fit is returned untouched, ellipsis and all.
	if got := truncate(s, len(s)); got != s {
		t.Errorf("truncate(%q, %d) = %q, want it unchanged", s, len(s), got)
	}
}

// TestFirstSentenceIsRuneSafe covers the caller that actually persists its
// result: the skill description column.
func TestFirstSentenceIsRuneSafe(t *testing.T) {
	instr := strings.Repeat("a", 218) + "—tail of the method"
	got := firstSentence(instr, 220)
	if !utf8.ValidString(got) {
		t.Errorf("firstSentence produced invalid UTF-8: %q", got)
	}
}

func TestParseProposalRejectsIncomplete(t *testing.T) {
	if _, err := parseProposal(json.RawMessage(`{"title":"No id or instruction"}`)); err == nil {
		t.Fatal("expected an error for a proposal missing id and instruction")
	}
	if _, err := parseProposal(json.RawMessage(`not json`)); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

// ---------------------------------------------------------------------------
// DB-backed tests
// ---------------------------------------------------------------------------

// testStore opens the configured Postgres, skipping when none is reachable.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed extraction test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// fakeCodex returns a CodexFunc that answers with the given JSON, so extraction
// is exercised end-to-end without spawning the engine.
func fakeCodex(t *testing.T, payload any) CodexFunc {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal fake codex payload: %v", err)
	}
	return func(_ context.Context, _ codex.Options) (codex.Result, error) {
		return codex.Result{OK: true, Text: string(b), JSON: b}, nil
	}
}

// seedPassedRun creates a run a person ACCEPTED — the only kind extraction is
// allowed to learn from, so it is what every happy-path test needs.
func seedPassedRun(t *testing.T, st *store.Store, runID, state string) {
	t.Helper()
	seedRun(t, st, runID, state, "run")
	acceptRun(t, st, runID, "accepted")
}

// seedRun creates the run and its nodes WITHOUT any outcome decision, so a test
// can say which verdict (if any) governs it. `mode` is "run" for real work and
// "plan" for a rehearsal.
func sdkFixture(runID, state, mode string) store.Run {
	return store.Run{RunID: runID, DAG: "market-scan", Goal: "size the note-taking market", State: state, Mode: mode,
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Compare rivals using consistent axes","nodes":[{"id":"gather","kind":"task","instruction":"Find rival offerings"},{"id":"verify","kind":"check","instruction":"Check claim citations"}]}}`),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"gather","phase":"passed","verification":"not_requested","findings":["three rivals, all subscription-priced"]},{"id":"verify","phase":"passed","findings":["every claim is cited"]}]}}`)}
}
func seedRun(t *testing.T, st *store.Store, runID, state, mode string) {
	t.Helper()
	if err := st.CreateRun(t.Context(), sdkFixture(runID, state, mode)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
}

// acceptRun records a run-grain Outcome Decision with the given verdict.
func acceptRun(t *testing.T, st *store.Store, runID, verdict string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id := store.OutcomeDecisionID(runID)
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID:        id,
		Title:     "Outcome · " + runID,
		Status:    "accepted",
		Decision:  verdict,
		Tags:      []string{store.OutcomeTag, verdict},
		Refs:      []string{"run:" + runID},
		Source:    "run:" + runID,
		DecidedAt: &now,
	}); err != nil {
		t.Fatalf("record outcome %q on %s: %v", verdict, runID, err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), id) })
}

// acceptJourney records a JOURNEY-grain verdict that names this run in its refs
// — the grain the inbox actually writes, and the one a run-grain-only lookup
// cannot see.
func acceptJourney(t *testing.T, st *store.Store, runID, verdict string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id := "outcome-journey-" + runID
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID:        id,
		Title:     "Delivery · " + runID,
		Status:    "accepted",
		Decision:  verdict,
		Tags:      []string{store.OutcomeTag, verdict, "journey"},
		Refs:      []string{"journey:" + runID, "run:" + runID},
		Source:    "journey:" + runID,
		DecidedAt: &now,
	}); err != nil {
		t.Fatalf("record journey outcome %q for %s: %v", verdict, runID, err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), id) })
}

// sampleProposal is the shape schemas/skill-extract.json constrains codex to.
func sampleProposal(id, title string) map[string]any {
	return map[string]any{
		"id":          id,
		"title":       title,
		"domain":      "research",
		"instruction": "Map every rival on the same feature axes before drawing a conclusion. Cite a source for each claim.",
		"doc":         "# Competitive teardown\n\n1. Fix the axes first.\n2. Fill the matrix.\n3. Cite everything.",
		"tags":        []string{"research", "teardown"},
		"confidence":  0.82,
		"rationale":   "The run's model review requested a citation per claim, which is the transferable bar.",
	}
}

func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestExtractSkillCreatesDraft(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-extract")
	skillID := uniqueID("competitive-teardown")
	seedPassedRun(t, st, runID, "passed")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	res, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, sampleProposal(skillID, "Competitive teardown")),
		RunDir: t.TempDir(),
	}, runID)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Status != ResultDrafted || res.Skill == nil {
		t.Fatalf("expected a drafted skill, got %+v", res)
	}

	stored, err := st.GetSkill(ctx, skillID)
	if err != nil {
		t.Fatalf("get stored skill: %v", err)
	}
	if !store.IsDraftSkill(stored) {
		t.Errorf("extracted skill must be a draft, got status %q", store.SkillStatus(stored))
	}
	if stored.Instruction == "" || stored.Category != "research" {
		t.Errorf("skill fields not persisted: %+v", stored)
	}

	var meta struct {
		Status     string   `json:"status"`
		SourceRun  string   `json:"sourceRun"`
		Confidence float64  `json:"confidence"`
		Doc        string   `json:"doc"`
		Tags       []string `json:"tags"`
	}
	if err := json.Unmarshal(stored.Metadata, &meta); err != nil {
		t.Fatalf("metadata is not an object: %v", err)
	}
	if meta.Status != store.SkillStatusDraft || meta.SourceRun != runID {
		t.Errorf("draft marker not written: %+v", meta)
	}
	if meta.Confidence == 0 || meta.Doc == "" || len(meta.Tags) != 2 {
		t.Errorf("confidence/doc/tags not carried into metadata: %+v", meta)
	}
}

// TestExtractSkillEligibility is the rehearsal-honesty guard on the learning
// loop, at the choke point BOTH paths go through — the automatic one an
// acceptance fires and POST /api/runs/{id}/extract-skill.
//
// The regression it pins: draft `multi-perspective-concept-validation` was
// extracted from a run in `mode: plan` with outcome `null` and rendered as
// "Learned from a passed run you accepted". Two independent lies, so two
// independent refusals — a rehearsal executed nothing, and a run nobody decided
// on is not a delivery anybody judged worth keeping.
func TestExtractSkillEligibility(t *testing.T) {
	cases := []struct {
		name string
		// mode is the run's mode column: "run" for real work, "plan" for a dry run.
		mode string
		// verdict is the outcome decision recorded on the run, "" for none.
		verdict string
		// grain is where that verdict was recorded: "run" or "journey".
		grain   string
		wantErr bool
		// wantReason is a phrase the refusal must contain, so the message stays
		// something a person can act on rather than "not eligible".
		wantReason string
	}{
		{name: "real run a human accepted", mode: "run", verdict: "accepted", grain: "run"},
		{name: "accepted at journey grain", mode: "run", verdict: "accepted", grain: "journey"},
		{name: "rehearsal", mode: "plan", verdict: "accepted", grain: "run",
			wantErr: true, wantReason: "rehearsal"},
		{name: "rehearsal spelled dry-run", mode: "dry-run", verdict: "accepted", grain: "run",
			wantErr: true, wantReason: "rehearsal"},
		{name: "nobody decided", mode: "run", verdict: "", grain: "run",
			wantErr: true, wantReason: "nobody has recorded an outcome"},
		{name: "changes requested", mode: "run", verdict: "changes_requested", grain: "run",
			wantErr: true, wantReason: "not accepted"},
		{name: "rejected", mode: "run", verdict: "rejected", grain: "journey",
			wantErr: true, wantReason: "not accepted"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testStore(t)
			ctx := context.Background()
			runID := uniqueID("run-eligible")
			skillID := uniqueID("eligibility")
			seedRun(t, st, runID, "passed", tc.mode)
			t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

			switch {
			case tc.verdict == "":
				// no decision at either grain
			case tc.grain == "run":
				acceptRun(t, st, runID, tc.verdict)
			default:
				acceptJourney(t, st, runID, tc.verdict)
			}

			res, err := ExtractSkill(ctx, st, Extractor{
				Codex:  fakeCodex(t, sampleProposal(skillID, "Eligibility probe")),
				RunDir: t.TempDir(),
			}, runID)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("extraction should have been allowed: %v", err)
				}
				if res.Status != ResultDrafted {
					t.Fatalf("expected a draft, got %+v", res)
				}
				return
			}
			if !errors.Is(err, ErrRunNotEligible) {
				t.Fatalf("expected ErrRunNotEligible, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Errorf("refusal does not say why: want %q in %q", tc.wantReason, err.Error())
			}
			if _, gerr := st.GetSkill(ctx, skillID); gerr == nil {
				t.Error("a refused extraction must persist no draft")
			}
		})
	}
}

// TestExtractSkillRefusesItsOwnStandInRationale: the extraction told us, in its
// own words, that what it read was mocked. Stamping 0.9 on that and filing it as
// a proposal is the second half of D-8 — the run-mode check is a fact about the
// run, this is the extractor's confession about its evidence.
func TestExtractSkillRefusesItsOwnStandInRationale(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-standin")
	skillID := uniqueID("standin")
	seedPassedRun(t, st, runID, "passed")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	prop := sampleProposal(skillID, "Multi-perspective concept validation")
	prop["rationale"] = "the recorded outputs are sparse and mocked, so confidence is high rather than absolute"
	prop["confidence"] = 0.9

	_, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, prop),
		RunDir: t.TempDir(),
	}, runID)
	if !errors.Is(err, ErrRunNotEligible) {
		t.Fatalf("expected ErrRunNotEligible, got %v", err)
	}
	if _, gerr := st.GetSkill(ctx, skillID); gerr == nil {
		t.Error("a proposal built on admitted stand-in output must not be persisted")
	}
}

// TestExtractSkillCapsHedgedConfidence: a rationale that hedges on how much it
// had to go on keeps its draft — the run was real — but not its self-reported
// score.
func TestExtractSkillCapsHedgedConfidence(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-thin")
	skillID := uniqueID("thin")
	seedPassedRun(t, st, runID, "passed")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	prop := sampleProposal(skillID, "Thin but real")
	prop["rationale"] = "the run's outputs are sparse, so this is the method as far as it can be read"
	prop["confidence"] = 0.9

	if _, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, prop),
		RunDir: t.TempDir(),
	}, runID); err != nil {
		t.Fatalf("a hedged rationale about a REAL run is still learnable: %v", err)
	}
	stored, err := st.GetSkill(ctx, skillID)
	if err != nil {
		t.Fatalf("get stored skill: %v", err)
	}
	var meta struct {
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(stored.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Confidence > thinConfidenceCeiling {
		t.Errorf("a rationale that calls its own evidence sparse may not claim %.2f", meta.Confidence)
	}
}

// TestApproveSkillRefusesRehearsalProvenance: extraction refuses a rehearsal
// now, but the drafts made before that rule are still in the base. They stay —
// a draft is a record of what the platform once proposed — and they never get
// approved into every prompt on the strength of a run that executed nothing.
func TestApproveSkillRefusesRehearsalProvenance(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-rehearsed")
	skillID := uniqueID("legacy-rehearsal-draft")
	seedRun(t, st, runID, "passed", "plan")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	// Written directly, exactly as a pre-rule extraction left it.
	meta, _ := json.Marshal(map[string]any{
		"status":     store.SkillStatusDraft,
		"sourceRun":  runID,
		"sourceRuns": []string{runID},
		"confidence": 0.9,
	})
	if err := st.InsertSkill(ctx, store.Skill{
		ID: skillID, Name: "Legacy rehearsal draft",
		Description: "extracted before rehearsals were refused",
		Instruction: "do the thing",
		Metadata:    meta,
	}); err != nil {
		t.Fatalf("insert legacy draft: %v", err)
	}

	_, err := ApproveSkill(ctx, st, skillID)
	if !errors.Is(err, ErrRehearsalSource) {
		t.Fatalf("expected ErrRehearsalSource, got %v", err)
	}
	stored, gerr := st.GetSkill(ctx, skillID)
	if gerr != nil {
		t.Fatalf("the draft must survive the refusal: %v", gerr)
	}
	if !store.IsDraftSkill(stored) {
		t.Error("a refused approval must leave the skill a draft")
	}
	if runs := SourceRuns(stored); len(runs) != 1 || runs[0] != runID {
		t.Errorf("SourceRuns should name the run it was learned from, got %v", runs)
	}
}

func TestExtractSkillRefusesRunThatDidNotPass(t *testing.T) {
	st := testStore(t)
	runID := uniqueID("run-failed")
	seedPassedRun(t, st, runID, "failed")

	_, err := ExtractSkill(context.Background(), st, Extractor{
		Codex:  fakeCodex(t, sampleProposal(uniqueID("never"), "Never created")),
		RunDir: t.TempDir(),
	}, runID)
	if !errors.Is(err, ErrRunNotEligible) {
		t.Fatalf("expected ErrRunNotEligible, got %v", err)
	}
}

func TestExtractSkillRefusesFailedSDKReview(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-badgate")
	seedPassedRun(t, st, runID, "passed")
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET doc=jsonb_set(doc,'{sdk,nodes,1,phase}','"failed"'::jsonb) WHERE run_id=$1`, runID); err != nil {
		t.Fatal(err)
	}

	_, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, sampleProposal(uniqueID("never"), "Never created")),
		RunDir: t.TempDir(),
	}, runID)
	if !errors.Is(err, ErrRunNotEligible) {
		t.Fatalf("expected ErrRunNotEligible, got %v", err)
	}
}

func TestExtractSkillDetectsDuplicate(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runID := uniqueID("run-dupe")
	seedPassedRun(t, st, runID, "passed")

	existingID := uniqueID("competitive-teardown")
	existing := store.Skill{
		ID:          existingID,
		Name:        "Competitive teardown",
		Description: "Map rivals on shared axes.",
		Category:    "research",
		Instruction: "Compare rivals feature by feature.",
	}
	if err := st.UpsertSkill(ctx, existing); err != nil {
		t.Fatalf("seed existing skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), existingID) })

	proposedID := uniqueID("competitive-teardowns")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), proposedID) })

	res, err := ExtractSkill(ctx, st, Extractor{
		Codex:  fakeCodex(t, sampleProposal(proposedID, "Competitive teardown")),
		RunDir: t.TempDir(),
	}, runID)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Status != ResultDuplicate || res.Duplicate == nil {
		t.Fatalf("expected a duplicate verdict, got %+v", res)
	}
	if res.Duplicate.SkillID != existingID {
		t.Errorf("duplicate points at %q, want %q", res.Duplicate.SkillID, existingID)
	}
	if !strings.Contains(res.Duplicate.Advice, "optimize") {
		t.Errorf("duplicate advice should route to the refinement loop, got %q", res.Duplicate.Advice)
	}
	if _, err := st.GetSkill(ctx, proposedID); !errors.Is(err, store.ErrNotFound) {
		t.Error("a duplicate must not create a near-identical second skill")
	}
}

// brokenStore returns a store whose pool is already closed, standing in for a
// transient DB failure (connection dropped, pool exhausted, statement timeout).
func brokenStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed extraction test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	st.Close()
	return st
}

// TestFindDuplicateAbortsOnStoreError is the regression test for the severe
// dedupe bug: findDuplicate used to treat ANY GetSkill error as "no collision"
// and to `continue` past SearchSkills failures. A transient DB error therefore
// read as "this id is free", and the write that followed replaced an existing,
// APPROVED skill with a draft — removing it from every prompt. "Unknown" must
// abort, never authorise the write.
func TestFindDuplicateAbortsOnStoreError(t *testing.T) {
	st := brokenStore(t)
	prop := SkillProposal{
		ID:          "competitive-teardown",
		Title:       "Competitive teardown",
		Domain:      "research",
		Instruction: "Map every rival on the same axes.",
	}
	dup, err := findDuplicate(context.Background(), st, prop)
	if err == nil {
		t.Fatalf("a failing store must abort the dedupe, got dup=%+v err=nil", dup)
	}
	if dup != nil {
		t.Errorf("no duplicate verdict may be returned alongside an error, got %+v", dup)
	}
}

// TestExtractSkillAbortsWhenDedupeCannotRun proves the abort reaches the caller:
// nothing is persisted when the dedupe probe could not answer.
func TestExtractSkillAbortsWhenDedupeCannotRun(t *testing.T) {
	st := brokenStore(t)
	_, err := ExtractSkill(context.Background(), st, Extractor{
		Codex:  fakeCodex(t, sampleProposal("never-persisted", "Never persisted")),
		RunDir: t.TempDir(),
	}, "run-does-not-matter")
	if err == nil {
		t.Fatal("extraction against a failing store must return an error, not a draft")
	}
}

// TestPersistDraftRefusesExistingID is the second half of the fix: the id guard
// must not live solely in findDuplicate. Even called directly — a reordering, a
// skipped check, or a concurrent extraction that won the race — persistDraft
// refuses to write over a skill that already exists.
func TestPersistDraftRefusesExistingID(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	id := uniqueID("approved-method")
	approved := store.Skill{
		ID:          id,
		Name:        "Approved method",
		Description: "The approved description.",
		Category:    "research",
		Reasoning:   "high",
		Instruction: "The approved instruction that must survive.",
	}
	if err := st.UpsertSkill(ctx, approved); err != nil {
		t.Fatalf("seed approved skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	run := store.Run{RunID: "run-x", DAG: "market-scan"}
	prop := SkillProposal{
		ID:          id,
		Title:       "Impostor",
		Domain:      "research",
		Instruction: "The draft instruction that must NOT land.",
	}
	if _, err := persistDraft(ctx, st, run, prop, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("persistDraft over an existing id: got %v, want store.ErrConflict", err)
	}

	after, err := st.GetSkill(ctx, id)
	if err != nil {
		t.Fatalf("get skill after refused draft: %v", err)
	}
	if store.IsDraftSkill(after) {
		t.Error("the existing approved skill was stamped as a draft")
	}
	if after.Instruction != approved.Instruction || after.Name != approved.Name || after.Reasoning != approved.Reasoning {
		t.Errorf("the existing skill was overwritten: %+v", after)
	}
}

func TestApproveAndRejectSkill(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	draftID := uniqueID("draft-skill")
	draft := store.Skill{
		ID:          draftID,
		Name:        "Draft method",
		Instruction: "Do the method.",
		Metadata:    json.RawMessage(`{"status":"draft","sourceRun":"run-x","doc":"# doc"}`),
	}
	if err := st.UpsertSkill(ctx, draft); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), draftID) })
	// This draft was LEARNED (it names a source run), so it needs a practice
	// test before it may enter prompts — see TestApproveLearnedDraftNeedsEvalset
	// for the guardrail itself.
	seedEvalset(t, st, draftID)

	approved, err := ApproveSkill(ctx, st, draftID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if store.IsDraftSkill(approved) {
		t.Error("approve must clear the draft marker")
	}
	var meta map[string]any
	if err := json.Unmarshal(approved.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta["status"] != store.SkillStatusActive {
		t.Errorf("status = %v, want active", meta["status"])
	}
	if meta["doc"] != "# doc" || meta["sourceRun"] != "run-x" {
		t.Errorf("approve must preserve the rest of the metadata, got %v", meta)
	}
	if meta["approvedAt"] == nil {
		t.Error("approve should stamp approvedAt")
	}

	// Approving twice is a conflict, not a silent no-op.
	if _, err := ApproveSkill(ctx, st, draftID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("expected ErrNotDraft on a second approve, got %v", err)
	}
	// An approved skill can no longer be rejected as a draft.
	if err := RejectSkill(ctx, st, draftID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("expected ErrNotDraft when rejecting an approved skill, got %v", err)
	}

	// A real draft is removed entirely.
	rejectID := uniqueID("reject-skill")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: rejectID, Name: "Rejectable", Instruction: "x",
		Metadata: json.RawMessage(`{"status":"draft"}`),
	}); err != nil {
		t.Fatalf("seed reject draft: %v", err)
	}
	if err := RejectSkill(ctx, st, rejectID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := st.GetSkill(ctx, rejectID); !errors.Is(err, store.ErrNotFound) {
		t.Error("a rejected draft must be gone")
	}
}
