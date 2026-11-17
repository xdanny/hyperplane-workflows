package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A person who builds a workflow out of runs they already did should not lose
// those runs. Before this, saving a workflow started its history at zero while
// the runs that taught them what to save sat in a separate ad-hoc group.
//
// Attaching moves ONLY the organizational identity: `options.projectWorkflow`
// is rewritten to name the saved workflow. The frozen `request` — the
// definition that actually executed, its values, its checks — is never touched,
// so an attached run still reports exactly what it did. A run is attachable
// only when its executed definition is the SAME definition the saved workflow
// holds now (same `definitionKey`), which is what makes the regrouping a
// statement of fact rather than a relabelling.

// AdHocKeyPrefix marks a run group with no saved source: the backend derived
// its identity from the definition the run submitted.
const AdHocKeyPrefix = "definition:"

// AttachReason is the same for every attachable run: it is why the offer exists.
const AttachReason = "ran the same steps as this workflow, with no saved source"

// WorkflowDefinitionKey returns the identity key of a saved workflow's CURRENT
// definition, computed by the same projection that identifies runs. A run whose
// frozen definition hashes to this key executed these steps.
//
// Tokens are not substituted: the key describes the definition as saved. A
// workflow whose instructions carry `{slot:…}` tokens therefore matches only
// runs launched from that same unfilled definition — a filled run executed
// different text, and saying otherwise would be a claim about work nobody did.
func (s *Store) WorkflowDefinitionKey(ctx context.Context, w SDKWorkflow) (string, error) {
	request := map[string]any{"autoRepair": w.AutoRepair}
	switch w.Kind {
	case "nodes":
		request["workflow"], request["instruction"], request["nodes"] = "node-workflow", w.Instruction, w.Nodes
	case "task":
		request["workflow"], request["instruction"] = "checked-task", w.Instruction
		// A launch records the RESOLVED target folder, so the saved path is
		// resolved the same way here or an unchanged workflow would look
		// different from its own runs on any host with a symlinked temp folder.
		request["workspace"], request["check"] = resolvedTarget(w.TargetRoot), w.Check
	case "app-ideator":
		request["workflow"], request["domain"] = "app-ideator", w.Instruction
	default:
		return "", fmt.Errorf("%w: unsupported kind %q", ErrInvalidWorkflow, w.Kind)
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request})
	if err != nil {
		return "", err
	}
	var key *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT project_workflow_identity($1::jsonb,'','',NULL)->>'definitionKey'`, options).Scan(&key); err != nil {
		return "", fmt.Errorf("workflow definition key: %w", err)
	}
	if key == nil {
		return "", nil
	}
	return *key, nil
}

// resolvedTarget resolves a saved target folder the way a launch does, and
// falls back to the stored path when the folder is gone.
func resolvedTarget(target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// AttachableRuns lists the runs in the workflow's project that ran this exact
// definition without a saved source. Newest first, bounded.
func (s *Store) AttachableRuns(ctx context.Context, w SDKWorkflow, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	key, err := s.WorkflowDefinitionKey(ctx, w)
	if err != nil || key == "" {
		return []Run{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runListCols+` FROM runs
		WHERE options->>'runtime' = 'codex-sdk' AND project = $1
		  AND project_workflow_identity(options,dag,goal,doc)->>'definitionKey' = $2
		  AND left(project_workflow_identity(options,dag,goal,doc)->>'key', $4) = $3
		ORDER BY started_at DESC, run_id DESC LIMIT $5`,
		w.Project, key, AdHocKeyPrefix, len(AdHocKeyPrefix), limit)
	if err != nil {
		return nil, fmt.Errorf("attachable runs: %w", err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ErrNotAttachable explains why one run cannot join a saved workflow's history.
type ErrNotAttachable struct{ Reason string }

func (e *ErrNotAttachable) Error() string { return e.Reason }

// AttachRunToWorkflow regroups one run under a saved workflow, in one
// transaction with the journal entry that records it. It refuses a run that
// already has a saved source, ran a different definition, or is not this
// build's SDK runtime — the identity must stay something the record supports.
func (s *Store) AttachRunToWorkflow(ctx context.Context, w SDKWorkflow, runID string) error {
	definitionKey, err := s.WorkflowDefinitionKey(ctx, w)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var options JSON
	var runtime, project string
	err = tx.QueryRow(ctx, `SELECT options, COALESCE(options->>'runtime',''), project FROM runs WHERE run_id=$1 FOR UPDATE`, runID).
		Scan(&options, &runtime, &project)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("attach run %q: %w", runID, err)
	}
	if runtime != "codex-sdk" {
		return &ErrNotAttachable{Reason: "this run predates the SDK runtime, so its steps cannot be matched to a saved workflow"}
	}
	var identity struct {
		Key           string `json:"key"`
		Name          string `json:"name"`
		DefinitionKey string `json:"definitionKey"`
	}
	var raw JSON
	if err := tx.QueryRow(ctx, `SELECT project_workflow_identity(options,dag,goal,doc) FROM runs WHERE run_id=$1`, runID).Scan(&raw); err != nil {
		return fmt.Errorf("attach run %q: %w", runID, err)
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return err
	}
	if !strings.HasPrefix(identity.Key, AdHocKeyPrefix) {
		return &ErrNotAttachable{Reason: "this run already belongs to a saved workflow"}
	}
	if identity.DefinitionKey == "" || identity.DefinitionKey != definitionKey {
		return &ErrNotAttachable{Reason: "this run executed different steps from the ones this workflow holds now"}
	}
	if project != w.Project {
		return &ErrNotAttachable{Reason: "this run belongs to a different project"}
	}

	attached := map[string]any{
		"version":          1,
		"key":              SavedWorkflowKey(w.ID),
		"name":             w.Name,
		"sourceWorkflowId": w.ID,
		"definitionKey":    identity.DefinitionKey,
		"attachedAt":       time.Now().UTC().Format(time.RFC3339),
		"attachedFrom":     identity.Key,
	}
	patch, err := json.Marshal(map[string]any{"projectWorkflow": attached})
	if err != nil {
		return err
	}
	// jsonb || replaces exactly the projectWorkflow key. The frozen request,
	// ownership, model and budget stay byte-for-byte what the launch wrote.
	if _, err := tx.Exec(ctx, `UPDATE runs SET options = options || $2::jsonb WHERE run_id=$1`, runID, JSON(patch)); err != nil {
		return fmt.Errorf("attach run %q: %w", runID, err)
	}
	data, err := json.Marshal(map[string]any{"workflowId": w.ID, "workflowName": w.Name, "attachedFrom": identity.Key})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (run_id, seq, ts, level, type, node, data)
		VALUES ($1,(SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1),now(),'info','run.attached',NULL,$2)`,
		runID, JSON(data)); err != nil {
		return fmt.Errorf("attach run %q: %w", runID, err)
	}
	return tx.Commit(ctx)
}
