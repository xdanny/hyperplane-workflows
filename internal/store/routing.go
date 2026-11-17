package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RoutingPolicy maps a scope ("skill:<id>" or "kind:<nodeKind>") to an engine
// recommendation and the eval evidence that justified it.
type RoutingPolicy struct {
	Scope      string    `json:"scope"`
	Engine     string    `json:"engine"`
	Confidence *float64  `json:"confidence,omitempty"`
	Margin     *float64  `json:"margin,omitempty"`
	EvalRunA   string    `json:"eval_run_a,omitempty"`
	EvalRunB   string    `json:"eval_run_b,omitempty"`
	Note       string    `json:"note,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UpsertRoutingPolicy inserts or replaces the policy for the given scope.
func (s *Store) UpsertRoutingPolicy(ctx context.Context, p RoutingPolicy) error {
	return upsertRoutingPolicy(ctx, s.Pool, p)
}

func upsertRoutingPolicy(ctx context.Context, db evalWriter, p RoutingPolicy) error {
	_, err := db.Exec(ctx, `
		INSERT INTO routing_policy (scope, engine, confidence, margin, eval_run_a, eval_run_b, note, updated_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),now())
		ON CONFLICT (scope) DO UPDATE SET
			engine=EXCLUDED.engine,
			confidence=EXCLUDED.confidence,
			margin=EXCLUDED.margin,
			eval_run_a=EXCLUDED.eval_run_a,
			eval_run_b=EXCLUDED.eval_run_b,
			note=EXCLUDED.note,
			updated_at=now()`,
		p.Scope, p.Engine, p.Confidence, p.Margin, p.EvalRunA, p.EvalRunB, p.Note)
	if err != nil {
		return fmt.Errorf("upsert routing policy: %w", err)
	}
	return nil
}

// GetRoutingPolicy fetches the policy for the given scope.
// Returns ErrNotFound when no policy exists.
func (s *Store) GetRoutingPolicy(ctx context.Context, scope string) (RoutingPolicy, error) {
	var p RoutingPolicy
	var evalRunA, evalRunB, note *string
	err := s.Pool.QueryRow(ctx, `
		SELECT scope, engine, confidence, margin, eval_run_a, eval_run_b, note, updated_at
		FROM routing_policy WHERE scope=$1`, scope).Scan(
		&p.Scope, &p.Engine, &p.Confidence, &p.Margin, &evalRunA, &evalRunB, &note, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, fmt.Errorf("get routing policy: %w", err)
	}
	p.EvalRunA = deref(evalRunA)
	p.EvalRunB = deref(evalRunB)
	p.Note = deref(note)
	return p, nil
}

// ListRoutingPolicies returns all routing policies ordered by scope.
func (s *Store) ListRoutingPolicies(ctx context.Context) ([]RoutingPolicy, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT scope, engine, confidence, margin, eval_run_a, eval_run_b, note, updated_at
		FROM routing_policy ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("list routing policies: %w", err)
	}
	defer rows.Close()
	var out []RoutingPolicy
	for rows.Next() {
		var p RoutingPolicy
		var evalRunA, evalRunB, note *string
		if err := rows.Scan(&p.Scope, &p.Engine, &p.Confidence, &p.Margin, &evalRunA, &evalRunB, &note, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.EvalRunA = deref(evalRunA)
		p.EvalRunB = deref(evalRunB)
		p.Note = deref(note)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteRoutingPolicy removes a routing policy by scope.
func (s *Store) DeleteRoutingPolicy(ctx context.Context, scope string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM routing_policy WHERE scope=$1`, scope)
	if err != nil {
		return fmt.Errorf("delete routing policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
