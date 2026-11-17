package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const recordCols = `type, id, state, gate_key, run_id, kind, consumed, created_at, updated_at, doc`

// UpsertRecord inserts or updates a global record (approval/notification/proposal/draft).
func (s *Store) UpsertRecord(ctx context.Context, r Record) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO records (type, id, state, gate_key, run_id, kind, consumed, created_at, updated_at, doc)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,now(),now(),$8)
		ON CONFLICT (type, id) DO UPDATE SET
			state=EXCLUDED.state, gate_key=EXCLUDED.gate_key, run_id=EXCLUDED.run_id,
			kind=EXCLUDED.kind, consumed=EXCLUDED.consumed, updated_at=now(), doc=EXCLUDED.doc`,
		r.Type, r.ID, r.State, r.GateKey, r.RunID, r.Kind, r.Consumed, jsonOr(r.Doc))
	if err != nil {
		return fmt.Errorf("upsert record: %w", err)
	}
	return nil
}

// ClaimRecord inserts a record and REFUSES to touch one that already exists,
// returning ErrExists instead. It is the insert-only counterpart to
// UpsertRecord, for callers that need a record to double as a CLAIM: the write
// either wins (and the caller owns the work) or reports that somebody already
// owns it. Automatic skill extraction claims one per accepted delivery so a
// billed codex call can never be fired twice for the same acceptance.
func (s *Store) ClaimRecord(ctx context.Context, r Record) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO records (type, id, state, gate_key, run_id, kind, consumed, created_at, updated_at, doc)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,now(),now(),$8)
		ON CONFLICT (type, id) DO NOTHING`,
		r.Type, r.ID, r.State, r.GateKey, r.RunID, r.Kind, r.Consumed, jsonOr(r.Doc))
	if err != nil {
		return fmt.Errorf("claim record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim record %s/%s: %w", r.Type, r.ID, ErrExists)
	}
	return nil
}

func scanRecord(row pgx.Row) (Record, error) {
	var r Record
	var state, gateKey, runID, kind *string
	err := row.Scan(&r.Type, &r.ID, &state, &gateKey, &runID, &kind, &r.Consumed,
		&r.CreatedAt, &r.UpdatedAt, &r.Doc)
	if err != nil {
		return r, err
	}
	r.State = deref(state)
	r.GateKey = deref(gateKey)
	r.RunID = deref(runID)
	r.Kind = deref(kind)
	return r, nil
}

// GetRecord fetches one record.
func (s *Store) GetRecord(ctx context.Context, typ, id string) (Record, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+recordCols+` FROM records WHERE type=$1 AND id=$2`, typ, id)
	r, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListRecords returns records of a type, optionally filtered by state (empty = all).
func (s *Store) ListRecords(ctx context.Context, typ, state string) ([]Record, error) {
	q := `SELECT ` + recordCols + ` FROM records WHERE type=$1`
	args := []any{typ}
	if state != "" {
		q += " AND state=$2"
		args = append(args, state)
	}
	q += " ORDER BY updated_at DESC"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecord removes a record.
func (s *Store) DeleteRecord(ctx context.Context, typ, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM records WHERE type=$1 AND id=$2`, typ, id)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DismissStaleRecords dismisses all pending records whose run is in a terminal or non-blocked state.
// Runs whose state is not 'blocked' or 'running' are considered stale.
func (s *Store) DismissStaleRecords(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE records SET state='dismissed', updated_at=now()
		WHERE state='pending'
		  AND run_id IS NOT NULL
		  AND run_id NOT IN (
		    SELECT run_id FROM runs WHERE state IN ('running','blocked')
		  )`)
	if err != nil {
		return 0, fmt.Errorf("dismiss stale records: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DismissRecord sets one record to 'dismissed'.
func (s *Store) DismissRecord(ctx context.Context, typ, id string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE records SET state='dismissed', updated_at=now()
		WHERE type=$1 AND id=$2 AND state='pending'`, typ, id)
	if err != nil {
		return fmt.Errorf("dismiss record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
