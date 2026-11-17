package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertNode inserts or updates a node's state.
func (s *Store) UpsertNode(ctx context.Context, n Node) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO nodes (run_id, node_id, kind, role, state, attempt, score, error, output, updated_at, doc)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),$10)
		ON CONFLICT (run_id, node_id) DO UPDATE SET
			kind=EXCLUDED.kind, role=EXCLUDED.role, state=EXCLUDED.state,
			attempt=EXCLUDED.attempt, score=EXCLUDED.score, error=EXCLUDED.error,
			output=EXCLUDED.output, updated_at=now(), doc=EXCLUDED.doc`,
		n.RunID, n.NodeID, n.Kind, n.Role, n.State, n.Attempt, n.Score, n.Error,
		jsonOr(n.Output), jsonOr(n.Doc))
	if err != nil {
		return fmt.Errorf("upsert node: %w", err)
	}
	return nil
}

func scanNode(row pgx.Row) (Node, error) {
	var n Node
	var role, errStr *string
	err := row.Scan(&n.RunID, &n.NodeID, &n.Kind, &role, &n.State, &n.Attempt,
		&n.Score, &errStr, &n.Output, &n.UpdatedAt, &n.Doc)
	if err != nil {
		return n, err
	}
	n.Role = deref(role)
	n.Error = deref(errStr)
	return n, nil
}

const nodeCols = `run_id, node_id, kind, role, state, attempt, score, error, output, updated_at, doc`

// GetNode fetches one node.
func (s *Store) GetNode(ctx context.Context, runID, nodeID string) (Node, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE run_id=$1 AND node_id=$2`, runID, nodeID)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// ListNodes returns all nodes for a run.
func (s *Store) ListNodes(ctx context.Context, runID string) ([]Node, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+nodeCols+` FROM nodes WHERE run_id=$1 ORDER BY node_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AppendEvent appends a journal event, assigning the next per-run seq atomically.
// Returns the assigned seq.
func (s *Store) AppendEvent(ctx context.Context, e Event) (int64, error) {
	if e.Level == "" {
		e.Level = "info"
	}
	var seq int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO events (run_id, seq, ts, level, type, node, data)
		VALUES ($1,
			(SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1),
			now(), $2, $3, NULLIF($4,''), $5)
		RETURNING seq`,
		e.RunID, e.Level, e.Type, e.Node, jsonOr(e.Data)).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("append event: %w", err)
	}
	return seq, nil
}

// ListEvents returns events for a run after sinceSeq (0 for all), up to limit.
func (s *Store) ListEvents(ctx context.Context, runID string, sinceSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT run_id, seq, ts, level, type, node, data
		FROM events WHERE run_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`,
		runID, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return scanEvents(rows)
}

// scanEvents drains an event query into Event values.
func scanEvents(rows pgx.Rows) ([]Event, error) {
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var node *string
		var ts time.Time
		if err := rows.Scan(&e.RunID, &e.Seq, &ts, &e.Level, &e.Type, &node, &e.Data); err != nil {
			return nil, err
		}
		e.TS = ts
		e.Node = deref(node)
		out = append(out, e)
	}
	return out, rows.Err()
}
