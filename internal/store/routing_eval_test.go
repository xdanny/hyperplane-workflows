package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRoutingEvaluationAtomicReceiptsAndSnapshotGuard(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	for _, mode := range []string{"success", "skill-changed", "cases-changed", "second-insert-fails"} {
		t.Run(mode, func(t *testing.T) {
			id := fmt.Sprintf("routing-atomic-%s-%d", mode, time.Now().UnixNano())
			sk := Skill{ID: id, Name: id, Instruction: "Exact evaluated instruction"}
			es := Evalset{ID: id, Name: id, Cases: []EvalCase{{ID: "c1", Input: json.RawMessage(`{"n": 1}`), Checks: []EvalCheckSpec{{Kind: "json-parse"}}}}}
			if err := st.UpsertSkill(ctx, sk); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertEvalset(ctx, es); err != nil {
				t.Fatal(err)
			}
			a := EvalRun{ID: id + "-a", EvalsetID: id, Target: sk.Instruction, Engine: "local", StartedAt: time.Now(), EndedAt: time.Now(), Status: "done", PassRate: 1, MeanScore: 1, Results: []EvalCaseResult{{Case: &es.Cases[0], CaseID: "c1", Passed: true, Score: 1, Output: json.RawMessage(`{}`)}}}
			b := a
			b.ID = id + "-b"
			b.Engine = "codex"
			policy := RoutingPolicy{Scope: "skill:" + id, Engine: "codex-local", EvalRunA: a.ID, EvalRunB: b.ID}
			if err := st.UpsertRoutingPolicy(ctx, RoutingPolicy{Scope: policy.Scope, Engine: "codex", Note: "prior"}); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "skill-changed":
				sk.Instruction = "New instruction"
				if err := st.UpsertSkill(ctx, sk); err != nil {
					t.Fatal(err)
				}
			case "cases-changed":
				changed := es
				changed.Cases = []EvalCase{{ID: "new"}}
				if err := st.UpsertEvalset(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "second-insert-fails":
				if err := st.SaveEvalRun(ctx, b); err != nil {
					t.Fatal(err)
				}
			}
			err := st.SaveRoutingEvaluation(ctx, policy, es, a.Target, a, b)
			got, readErr := st.GetRoutingPolicy(ctx, policy.Scope)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "success" {
				if err != nil || got.EvalRunA != a.ID || got.EvalRunB != b.ID {
					t.Fatalf("commit missing: %+v %v", got, err)
				}
				for _, id := range []string{a.ID, b.ID} {
					receipt, err := st.GetEvalRun(ctx, id)
					if err != nil || receipt.Results[0].Case == nil || receipt.Target != a.Target {
						t.Fatalf("snapshot missing: %+v %v", receipt, err)
					}
				}
			} else {
				if err == nil || got.Note != "prior" {
					t.Fatalf("failed transaction changed policy: %+v %v", got, err)
				}
				if mode != "second-insert-fails" && !errors.Is(err, ErrRoutingEvaluationChanged) {
					t.Fatalf("wrong stale result: %v", err)
				}
				if _, err := st.GetEvalRun(ctx, a.ID); err == nil {
					t.Fatal("first receipt escaped failed transaction")
				}
			}
		})
	}
}
