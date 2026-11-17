package httpapi_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A learned skill cannot be approved until it has a practice test, and the only
// way to make one used to be a terminal command. The endpoint runs the same
// generator the CLI runs, and refuses to overwrite one silently.
func TestGeneratePracticeTestForSkill(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "httpapi", "testdata", "evalset-generator"))
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()

	skillID := "constrained-short-form-drafting-" + time.Now().UTC().Format("150405.000000")
	c.call("POST", "/api/skills", store.Skill{ID: skillID, Name: "Constrained short-form drafting",
		Instruction: "Cut to the decision, then the two facts that support it."}, 200, nil)
	t.Cleanup(func() {
		_ = st.DeleteSkill(t.Context(), skillID)
		_, _ = st.Pool.Exec(t.Context(), `DELETE FROM evalsets WHERE skill_ref=$1`, skillID)
	})

	var generated struct {
		EvalsetID string `json:"evalsetId"`
		Cases     []struct {
			ID       string `json:"id"`
			Criteria string `json:"criteria"`
		} `json:"cases"`
	}
	c.call("POST", "/api/skills/"+skillID+"/evalset/generate", map[string]any{"engine": "codex"}, 201, &generated)
	if generated.EvalsetID == "" || len(generated.Cases) != 2 {
		t.Fatalf("generated %+v", generated)
	}
	if generated.Cases[0].Criteria == "" {
		t.Fatal("a case without criteria tests nothing")
	}
	stored, err := st.GetEvalset(t.Context(), generated.EvalsetID)
	if err != nil {
		t.Fatalf("the practice test must be stored: %v", err)
	}
	if stored.SkillRef != skillID || len(stored.Cases) != 2 {
		t.Fatalf("stored %+v", stored)
	}

	// A second call does not quietly replace the test somebody may have edited.
	var conflict struct {
		EvalsetID string `json:"evalsetId"`
		Error     string `json:"error"`
	}
	c.call("POST", "/api/skills/"+skillID+"/evalset/generate", map[string]any{}, 409, &conflict)
	if conflict.EvalsetID != generated.EvalsetID || conflict.Error == "" {
		t.Fatalf("conflict %+v", conflict)
	}
	c.call("POST", "/api/skills/"+skillID+"/evalset/generate", map[string]any{"replace": true}, 201, &generated)

	// Unknown engines and unknown skills are refused before anything is billed.
	c.call("POST", "/api/skills/"+skillID+"/evalset/generate", map[string]any{"engine": "wishful"}, 400, nil)
	c.call("POST", "/api/skills/no-such-skill/evalset/generate", map[string]any{"replace": true}, 404, nil)
}
