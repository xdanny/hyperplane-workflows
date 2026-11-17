package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrExists is returned by insert-only writes when the row is already present
// (e.g. registering a process id+version that is already in the registry).
var ErrExists = errors.New("already exists")

func jsonOr(j JSON) JSON {
	if len(j) == 0 {
		return JSON("{}")
	}
	return j
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// CreateRun inserts a new run.
func (s *Store) CreateRun(ctx context.Context, r Run) error {
	// Auto-register the run's project tag so the projects table stays in sync
	// with what is actually tagged (no-op for an unscoped run).
	if err := s.EnsureProject(ctx, r.Project); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO runs (run_id, dag, goal, state, mode, type, domain, risk_level, parent_run,
			started_at, ended_at, updated_at, owner_pid, options, summary, metadata, doc, project)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,now(),$12,$13,$14,$15,$16,$17)`,
		r.RunID, r.DAG, r.Goal, r.State, r.Mode, r.Type, r.Domain, r.RiskLevel, r.ParentRun,
		timePtr(r.StartedAt), timePtr(r.EndedAt), r.OwnerPID,
		jsonOr(r.Options), jsonOr(r.Summary), jsonOr(r.Metadata), jsonOr(r.Doc), r.Project)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

// UpdateRun updates mutable run fields and bumps updated_at.
func (s *Store) UpdateRun(ctx context.Context, r Run) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE runs SET goal=NULLIF($2,''), state=$3, mode=$4, type=$5, domain=$6, risk_level=$7,
			started_at=$8, ended_at=$9, updated_at=now(), owner_pid=$10,
			options=$11, summary=$12, metadata=$13, doc=$14
		WHERE run_id=$1`,
		r.RunID, r.Goal, r.State, r.Mode, r.Type, r.Domain, r.RiskLevel,
		timePtr(r.StartedAt), timePtr(r.EndedAt), r.OwnerPID,
		jsonOr(r.Options), jsonOr(r.Summary), jsonOr(r.Metadata), jsonOr(r.Doc))
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRunState transitions a run's state (and ended_at for terminal states).
func (s *Store) SetRunState(ctx context.Context, runID, state string) error {
	terminal := state == "passed" || state == "failed" || state == "interrupted"
	tag, err := s.Pool.Exec(ctx, `
		UPDATE runs SET state=$2, updated_at=now(),
			ended_at = CASE WHEN $3 THEN now() ELSE ended_at END
		WHERE run_id=$1`, runID, state, terminal)
	if err != nil {
		return fmt.Errorf("set run state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var goal, mode, typ, domain, risk, parent *string
	var started, ended *time.Time
	var ownerPID *int
	err := row.Scan(&r.RunID, &r.DAG, &goal, &r.State, &mode, &typ, &domain, &risk, &parent,
		&started, &ended, &r.UpdatedAt, &ownerPID,
		&r.Options, &r.Summary, &r.Metadata, &r.Doc, &r.Project)
	if err != nil {
		return r, err
	}
	r.Goal = deref(goal)
	r.Mode = deref(mode)
	r.Type = deref(typ)
	r.Domain = deref(domain)
	r.RiskLevel = deref(risk)
	r.ParentRun = deref(parent)
	if started != nil {
		r.StartedAt = *started
	}
	if ended != nil {
		r.EndedAt = *ended
	}
	if ownerPID != nil {
		r.OwnerPID = *ownerPID
	}
	return r, nil
}

const runCols = `run_id, dag, goal, state, mode, type, domain, risk_level, parent_run,
	started_at, ended_at, updated_at, owner_pid, options, summary, metadata, doc, project`

// runListCols is runCols with the launch record's `spec` stripped out of
// options. A run stores the exact DAG source it executed there so a bounded
// Repair can replay the same promises (see engine.launchRecord), and some specs
// run to tens of kilobytes — returning one per row would make every list poll
// pay for a payload no list view reads. GetRun keeps the full options.
const runListCols = `run_id, dag, goal, state, mode, type, domain, risk_level, parent_run,
	started_at, ended_at, updated_at, owner_pid, (options - 'spec') AS options,
	summary, metadata, doc, project`

// GetRun fetches a single run.
func (s *Store) GetRun(ctx context.Context, runID string) (Run, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+runCols+` FROM runs WHERE run_id=$1`, runID)
	r, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListRuns returns runs newest-first, optionally filtered.
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	var where []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.State != "" {
		add("state=$%d", f.State)
	}
	if f.Runtime != "" {
		add("options->>'runtime'=$%d", f.Runtime)
	}
	if f.DAG != "" {
		add("dag=$%d", f.DAG)
	}
	if f.Type != "" {
		add("type=$%d", f.Type)
	}
	if f.Parent != "" {
		add("parent_run=$%d", f.Parent)
	}
	if f.TopLevel {
		// Continuations are delivery attempts even though their source is retained
		// as parent_run. Ordinary nested/batch children remain outside this view.
		where = append(where, "(parent_run IS NULL OR (options->>'runtime'='codex-sdk' AND options#>>'{continuation,sourceRunId}'=parent_run AND parent_run<>run_id))")
	}
	if f.Project != nil {
		add("project=$%d", *f.Project)
	}
	if f.Recipe != "" {
		add("doc->>'"+RecipeKey+"'=$%d", f.Recipe)
	}
	q := `SELECT ` + runListCols + ` FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	if f.ByStart {
		q += " ORDER BY started_at DESC, run_id DESC"
	} else {
		q += " ORDER BY updated_at DESC"
	}
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FailingNodesByRun returns, for each of the given runs, the id of the node
// that best explains why it did not pass — so a run LIST can say "failed at
// report-verify" instead of making the reader open the run to find out.
//
// One node per run, chosen by:
//  1. failed before blocked — a failed node has a verdict to read, a blocked one
//     is waiting on a person;
//  2. a non-`process` node before a `process` node — a process node only ever
//     fails because the sub-run beneath it did, so naming it says nothing;
//  3. earliest updated — the first thing that broke, not the last.
//
// One query for the whole page. Runs with no failing node are simply absent
// from the map.
func (s *Store) FailingNodesByRun(ctx context.Context, runIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (run_id) run_id, node_id
		FROM nodes
		WHERE run_id = ANY($1) AND state IN ('failed','blocked')
		ORDER BY run_id, (state <> 'failed'), (kind = 'process'), updated_at`, runIDs)
	if err != nil {
		return nil, fmt.Errorf("failing nodes by run: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID, nodeID string
		if err := rows.Scan(&runID, &nodeID); err != nil {
			return nil, err
		}
		out[runID] = nodeID
	}
	return out, rows.Err()
}

// ErrRunActive prevents removal while execution or its ownership session remains active.
var ErrRunActive = errors.New("run is still active; cancel it and wait for execution to stop before deleting")

// DeleteRun removes a stopped run and its nodes/events. The transaction lock
// excludes concurrent SDK admission for this identity until deletion commits.
func (s *Store) DeleteRun(ctx context.Context, runID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,684795135))`, runID).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrRunActive
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM runs WHERE run_id=$1 FOR UPDATE`, runID).Scan(&state); err == pgx.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state == "running" {
		return ErrRunActive
	}
	if _, err := tx.Exec(ctx, `DELETE FROM runs WHERE run_id=$1`, runID); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	return tx.Commit(ctx)
}

// RunsByParent returns all runs whose parent_run equals parentRunID, newest first.
func (s *Store) RunsByParent(ctx context.Context, parentRunID string) ([]Run, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+runListCols+` FROM runs WHERE parent_run=$1 ORDER BY updated_at DESC`,
		parentRunID)
	if err != nil {
		return nil, fmt.Errorf("runs by parent: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// pidAlive returns true when pid is a live process on this machine.
// Uses kill(pid, 0): nil or EPERM → alive; ESRCH → dead.
// pid == 0 is treated as dead (unknown owner).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true // signal delivered → process exists
	}
	if err == syscall.EPERM {
		return true // not permitted to signal → process exists, different user
	}
	return false // ESRCH: no such process → dead
}

// ReconcileOrphans interrupts abandoned runs without replaying any work. Current
// SDK owners hold a PostgreSQL session lock; PID checks apply only to old records.
// Each transition, delivery snapshot, approval dismissal and event commit together.
func (s *Store) ReconcileOrphans(ctx context.Context) (int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT run_id, COALESCE(owner_pid,0),
		COALESCE(options->>'ownership','') FROM runs WHERE state='running'`)
	if err != nil {
		return 0, fmt.Errorf("reconcile orphans: query: %w", err)
	}
	type candidate struct {
		id, ownership string
		pid           int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.pid, &c.ownership); err != nil {
			rows.Close()
			return 0, err
		}
		if c.ownership == SDKOwnership || !pidAlive(c.pid) {
			candidates = append(candidates, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var count int64
	for _, c := range candidates {
		changed, err := s.reconcileRun(ctx, c.id, c.ownership == SDKOwnership)
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func (s *Store) reconcileRun(ctx context.Context, id string, sessionOwned bool) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if sessionOwned {
		var acquired bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 684795135))`, id).Scan(&acquired); err != nil {
			return false, err
		}
		if !acquired {
			return false, nil
		}
	}
	changed, err := interruptRun(ctx, tx, id, "Execution owner disconnected; work was not replayed")
	if err != nil || !changed {
		return changed, err
	}
	return true, tx.Commit(ctx)
}

// interruptRun completes cancellation and orphan recovery in the caller's
// transaction. The run row serializes checkpoint and human-decision writers.
func interruptRun(ctx context.Context, tx pgx.Tx, id, reason string) (bool, error) {
	var sdk bool
	var snapshot JSON
	err := tx.QueryRow(ctx, `UPDATE runs SET state='interrupted', ended_at=now(), updated_at=now(),
		doc = CASE WHEN options->>'runtime'='codex-sdk' THEN doc || jsonb_build_object('sdk',
			COALESCE(doc->'sdk','{}'::jsonb) || jsonb_build_object('phase','interrupted','passed',false,'error',$2::text)) ELSE doc END,
		summary = CASE WHEN options->>'runtime'='codex-sdk' THEN COALESCE(doc->'sdk','{}'::jsonb) ||
			jsonb_build_object('phase','interrupted','passed',false,'error',$2::text) ELSE summary END
		WHERE run_id=$1 AND state='running'
		RETURNING COALESCE(options->>'runtime'='codex-sdk',false),COALESCE(doc->'sdk','{}'::jsonb)`, id, reason).Scan(&sdk, &snapshot)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if sdk {
		if err := finishSDKTicket(ctx, tx, id, "interrupted", snapshot); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO nodes (run_id,node_id,kind,role,state,doc,output,error)
			VALUES ($1,'delivery','sdk','generate','interrupted',$2,'{}'::jsonb,$3)
			ON CONFLICT (run_id,node_id) DO UPDATE SET state='interrupted',updated_at=now(),doc=EXCLUDED.doc,output='{}'::jsonb,error=EXCLUDED.error`, id, snapshot, reason); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events (run_id,seq,type,node,data)
			VALUES ($1,(SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1),'run.done','delivery',$2)`, id, snapshot); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE records SET state='dismissed',updated_at=now()
		WHERE run_id=$1 AND state='pending'`, id); err != nil {
		return false, err
	}
	return true, nil
}

// CancelRun transitions a running run to interrupted (cancelled).
// Returns ErrNotFound if the run does not exist, or a sentinel error if the run
// is already in a terminal state and cannot be cancelled.
func (s *Store) CancelRun(ctx context.Context, runID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	changed, err := interruptRun(ctx, tx, runID, "Execution cancelled; work was not replayed")
	if err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	if !changed {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=$1)`, runID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return ErrNotRunning
	}
	return tx.Commit(ctx)
}

// ErrNotRunning is returned by CancelRun when the run is not in the running state.
var ErrNotRunning = errors.New("run is not running")

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
