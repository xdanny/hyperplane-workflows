package store

import (
	"context"
	"fmt"
)

// RunOrdinals numbers each given run among the runs of the same workflow in the
// same project, oldest first: "run 3 of Weekly competitor brief" instead of a
// relative time and an id tail. Grouping uses the SAME projection the project
// summaries use, so a run saved, rerun and relaunched from its saved source
// stays in one series.
//
// One query for the whole page. Runs that are not codex-sdk launches, or whose
// identity cannot be projected, are simply absent from the map.
func (s *Store) RunOrdinals(ctx context.Context, runIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH scope AS (
			SELECT DISTINCT project, project_workflow_identity(options,dag,goal,doc)->>'key' AS key
			FROM runs WHERE run_id = ANY($1) AND options->>'runtime' = 'codex-sdk'
		), numbered AS (
			SELECT r.run_id, row_number() OVER (
				PARTITION BY r.project, project_workflow_identity(r.options,r.dag,r.goal,r.doc)->>'key'
				ORDER BY r.started_at, r.run_id) AS ordinal
			FROM runs r JOIN scope s
			  ON s.project = r.project
			 AND s.key = project_workflow_identity(r.options,r.dag,r.goal,r.doc)->>'key'
			WHERE r.options->>'runtime' = 'codex-sdk'
		)
		SELECT run_id, ordinal FROM numbered WHERE run_id = ANY($1)`, runIDs)
	if err != nil {
		return nil, fmt.Errorf("run ordinals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var ordinal int64
		if err := rows.Scan(&runID, &ordinal); err != nil {
			return nil, err
		}
		out[runID] = int(ordinal)
	}
	return out, rows.Err()
}

// SavedWorkflowKey is the grouping key a run carries when it was launched from
// a saved workflow. It is assigned by the project_workflow_identity projection.
func SavedWorkflowKey(workflowID string) string { return "saved:" + workflowID }

// RunsForSavedWorkflow returns the runs launched from one saved workflow,
// newest first, plus the total number of them. It reads the existing runs
// projection — there is no per-workflow run table and no history is rewritten.
func (s *Store) RunsForSavedWorkflow(ctx context.Context, workflowID string, limit int) ([]Run, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	key := SavedWorkflowKey(workflowID)
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs
		WHERE options->>'runtime' = 'codex-sdk'
		  AND project_workflow_identity(options,dag,goal,doc)->>'key' = $1`, key).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("saved workflow runs: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runListCols+` FROM runs
		WHERE options->>'runtime' = 'codex-sdk'
		  AND project_workflow_identity(options,dag,goal,doc)->>'key' = $1
		ORDER BY started_at DESC, run_id DESC LIMIT $2`, key, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("saved workflow runs: %w", err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
