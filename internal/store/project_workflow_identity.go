package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PrepareProjectWorkflow captures a source association, never saved execution
// settings or a claim that a saved revision ran. The SQL projection owns identity.
func (s *Store) PrepareProjectWorkflow(ctx context.Context, request json.RawMessage, sourceID, name, goal string) (JSON, error) {
	name = strings.TrimSpace(name)
	if len(name) > 160 || strings.ContainsRune(name, 0) || len(sourceID) > 160 || strings.ContainsRune(sourceID, 0) {
		return nil, fmt.Errorf("%w: workflow name and source ID must fit 160 bytes", ErrInvalidWorkflow)
	}
	if sourceID != "" {
		var sourceName string
		if err := s.Pool.QueryRow(ctx, `SELECT name FROM sdk_workflows WHERE id=$1`, sourceID).Scan(&sourceName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: saved workflow source is unavailable; choose 'Use as a new workflow' to run these settings without that association", ErrInvalidWorkflow)
			}
			return nil, err
		}
		if name == "" {
			name = sourceName
		}
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request, "sourceWorkflowId": sourceID, "workflowName": name})
	if err != nil {
		return nil, err
	}
	return s.ProjectWorkflowIdentity(ctx, options, "", goal, nil)
}

// ProjectWorkflowIdentity also reads older SDK requests through the exact same
// projection used by admission and project summaries. No model or SDK turn runs.
func (s *Store) ProjectWorkflowIdentity(ctx context.Context, options JSON, dag, goal string, doc JSON) (JSON, error) {
	var identity JSON
	err := s.Pool.QueryRow(ctx, `SELECT project_workflow_identity($1::jsonb,$2,$3,$4::jsonb)`, options, dag, goal, doc).Scan(&identity)
	return identity, err
}
