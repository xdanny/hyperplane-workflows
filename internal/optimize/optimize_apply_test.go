package optimize

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestOptimizeApplyPreservesConcurrentSkillEdits(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()
	for _, edit := range []string{"none", "instruction", "metadata"} {
		t.Run(edit, func(t *testing.T) {
			ctx := t.Context()
			id := "optimize-apply-" + time.Now().Format("150405.000000000")
			initial := store.Skill{ID: id, Name: "Evaluated skill", Instruction: "Original instruction", Metadata: store.JSON(`{"status":"active","note":"original"}`)}
			if err := st.UpsertSkill(ctx, initial); err != nil {
				t.Fatal(err)
			}
			expected, err := st.GetSkill(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			es := store.Evalset{ID: id + "-eval", Name: "Configured cases", SkillRef: id, Cases: []store.EvalCase{{ID: "case", Input: store.JSON(`{"task":"explain"}`), Criteria: "Explain concretely", Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}}}}
			if err := st.UpsertEvalset(ctx, es); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Messages []struct{ Content string } }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var parts []string
				for _, m := range body.Messages {
					parts = append(parts, m.Content)
				}
				prompt := strings.Join(parts, "\n")
				calls++
				answer := "Baseline answer"
				switch {
				case strings.Contains(prompt, "You are improving a skill instruction"):
					// This save occurs after Optimize loaded/evaluated the snapshot, before it applies a winner.
					if edit != "none" {
						changed := expected
						if edit == "instruction" {
							changed.Instruction = "Newer human instruction"
						} else {
							changed.Metadata = store.JSON(`{"status":"draft","note":"newer human metadata"}`)
						}
						if err := st.UpsertSkill(ctx, changed); err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						expected, err = st.GetSkill(ctx, id)
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
					answer = `["Winning instruction"]`
				case strings.Contains(prompt, "CRITERION:"):
					answer = `{"score":0.1,"pass":false,"reasons":["too vague"]}`
					if strings.Contains(prompt, "Improved answer") {
						answer = `{"score":0.9,"pass":true,"reasons":["concrete"]}`
					}
				case strings.Contains(prompt, "Winning instruction\n\nINPUT:"):
					answer = "Improved answer"
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			t.Setenv("LOCAL_LLM_BASE_URL", server.URL+"/v1")
			t.Setenv("LOCAL_LLM_API_KEY", "")
			result, applyErr := Optimize(ctx, st, Config{SkillID: id, Variants: 1, Proposer: "local", ScoreEngine: "local", Apply: true})
			if result.Winner == nil || result.Winner.Instruction != "Winning instruction" || calls != 5 {
				t.Fatalf("winner was not actually evaluated: %+v err=%v calls=%d", result, applyErr, calls)
			}
			current, err := st.GetSkill(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			runs, err := ListOptimizeRuns(ctx, st, id)
			if err != nil {
				t.Fatal(err)
			}
			if edit == "none" {
				if applyErr != nil || !result.Applied || current.Instruction != "Winning instruction" || string(current.Metadata) != string(expected.Metadata) || len(runs) != 1 {
					t.Fatalf("unchanged snapshot not applied: %+v skill=%+v runs=%d err=%v", result, current, len(runs), applyErr)
				}
			} else {
				if !errors.Is(applyErr, store.ErrSkillChanged) || result.Applied || !reflect.DeepEqual(current, expected) || len(runs) != 0 {
					t.Fatalf("newer %s overwritten or stale promotion recorded: %+v skill=%+v expected=%+v runs=%d err=%v", edit, result, current, expected, len(runs), applyErr)
				}
			}
		})
	}
}
