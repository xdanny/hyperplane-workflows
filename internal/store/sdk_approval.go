package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrSDKApprovalExpired = errors.New("this approval is no longer pending; refresh the run")

func (s *Store) DecideSDKApproval(ctx context.Context, id, decision string) (Record, error) {
	if decision != "approved" && decision != "rejected" {
		return Record{}, fmt.Errorf("invalid decision")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback(ctx)
	// The run is locked first, matching checkpoint/cancellation lock order.
	var runID string
	err = tx.QueryRow(ctx, `SELECT r.run_id FROM runs r JOIN records a ON a.run_id=r.run_id
 WHERE a.type='approvals' AND a.id=$1 AND a.kind='sdk-approval' AND r.state='running'
 AND r.doc->'sdk'->>'phase'='approval_pending' AND r.doc->'sdk'->'approval'->>'id'=a.id FOR UPDATE OF r`, id).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrSDKApprovalExpired
	}
	if err != nil {
		return Record{}, err
	}
	record, err := scanRecord(tx.QueryRow(ctx, `UPDATE records SET state=$2,doc=doc||jsonb_build_object('decision',$2::text),updated_at=now()
 WHERE type='approvals' AND id=$1 AND state='pending' AND consumed=false RETURNING `+recordCols, id, decision))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrSDKApprovalExpired
	}
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) ConsumeSDKApproval(ctx context.Context, runID, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize against cancellation before touching the decision. A snapshot
	// read in UPDATE ... FROM could otherwise consume an already-cancelled run.
	var current string
	err = tx.QueryRow(ctx, `SELECT run_id FROM runs WHERE run_id=$1 AND state='running'
		AND doc->'sdk'->>'phase'='approval_pending' AND doc->'sdk'->'approval'->>'id'=$2 FOR UPDATE`, runID, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSDKApprovalExpired
	}
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE records a SET consumed=true,updated_at=now()
 WHERE a.type='approvals' AND a.id=$1 AND a.run_id=$2 AND a.kind='sdk-approval'
	AND a.state IN ('approved','rejected') AND NOT a.consumed`, id, runID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrSDKApprovalExpired
	}
	return tx.Commit(ctx)
}
