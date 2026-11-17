package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type evalWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// SaveEvalRun writes a terminal result in one statement, including failed-run
// diagnostics. The case snapshots and Target retain the evaluated input contract.
func (s *Store) SaveEvalRun(ctx context.Context, run EvalRun) error {
	return insertEvalRun(ctx, s.Pool, run)
}
func insertEvalRun(ctx context.Context, db evalWriter, run EvalRun) error {
	if run.Status != "done" && run.Status != "failed" {
		return fmt.Errorf("eval receipt must be terminal")
	}
	results, err := json.Marshal(run.Results)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO eval_runs (id,evalset_id,target,engine,model,started_at,ended_at,results,pass_rate,mean_score,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, run.ID, run.EvalsetID, run.Target, run.Engine, run.Model, run.StartedAt, run.EndedAt, results, run.PassRate, run.MeanScore, run.Status)
	return err
}

var ErrRoutingEvaluationChanged = errors.New("skill or evalset changed during evaluation; review and evaluate again")

// SaveRoutingEvaluation commits both comparison receipts and their policy
// together, only while the exact evaluated skill and cases are still current.
func (s *Store) SaveRoutingEvaluation(ctx context.Context, policy RoutingPolicy, es Evalset, instruction string, a, b EvalRun) error {
	if !strings.HasPrefix(policy.Scope, "skill:") || policy.EvalRunA != a.ID || policy.EvalRunB != b.ID || a.ID == b.ID || a.Engine != "local" || b.Engine != "codex" || len(es.Cases) == 0 {
		return fmt.Errorf("invalid routing comparison identity")
	}
	for _, run := range []EvalRun{a, b} {
		if run.Status != "done" || run.EvalsetID != es.ID || run.Target != instruction || len(run.Results) != len(es.Cases) {
			return fmt.Errorf("routing needs complete comparable evaluations")
		}
		for i, result := range run.Results {
			if result.Case == nil || result.CaseID != es.Cases[i].ID || !reflect.DeepEqual(*result.Case, es.Cases[i]) {
				return fmt.Errorf("routing evaluation case snapshot differs")
			}
		}
	}
	for i, result := range a.Results {
		if strings.TrimSpace(es.Cases[i].Criteria) != "" && (result.Judge == nil || b.Results[i].Judge == nil || !reflect.DeepEqual(result.Judge, b.Results[i].Judge)) {
			return fmt.Errorf("routing needs the same identified judge configuration")
		}
	}
	selected := b
	switch policy.Engine {
	case "codex-local":
		selected = a
	case "codex":
	default:
		return fmt.Errorf("invalid selected routing engine")
	}
	if selected.PassRate <= 0 {
		return fmt.Errorf("selected engine did not pass a case")
	}
	if policy.Confidence != nil && *policy.Confidence != selected.PassRate {
		return fmt.Errorf("routing confidence differs from selected engine")
	}

	cases, err := json.Marshal(es.Cases)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, `SELECT instruction FROM skills WHERE id=$1 FOR UPDATE`, strings.TrimPrefix(policy.Scope, "skill:")).Scan(&current); err != nil {
		return err
	}
	if current != instruction {
		return ErrRoutingEvaluationChanged
	}
	var same bool
	if err := tx.QueryRow(ctx, `SELECT cases=$2::jsonb FROM evalsets WHERE id=$1 FOR UPDATE`, es.ID, cases).Scan(&same); err != nil {
		return err
	}
	if !same {
		return ErrRoutingEvaluationChanged
	}
	if err := insertEvalRun(ctx, tx, a); err != nil {
		return err
	}
	if err := insertEvalRun(ctx, tx, b); err != nil {
		return err
	}
	if err := upsertRoutingPolicy(ctx, tx, policy); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
