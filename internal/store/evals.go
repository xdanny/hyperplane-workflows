package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EvalCheckSpec is a local copy to avoid importing internal/dag.
type EvalCheckSpec struct {
	Kind string `json:"kind"`
	Min  *int   `json:"min,omitempty"`
	Max  *int   `json:"max,omitempty"`
	Key  string `json:"key,omitempty"`
}

type EvalCase struct {
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
	Criteria string          `json:"criteria,omitempty"`
	Expect   json.RawMessage `json:"expect,omitempty"`
	Checks   []EvalCheckSpec `json:"checks,omitempty"`
}

// EvalJudgeConfig identifies the requested scorer configuration, not immutable
// hosted model weights. Empty model plus ModelDefault explicitly records delegation.
type EvalJudgeConfig struct {
	Provider            string `json:"provider"`
	Model               string `json:"model,omitempty"`
	ModelDefault        bool   `json:"model_default,omitempty"`
	EndpointFingerprint string `json:"endpoint_fingerprint,omitempty"`
	RubricFingerprint   string `json:"rubric_fingerprint"`
}

type EvalCaseResult struct {
	Judge   *EvalJudgeConfig `json:"judge,omitempty"`
	Case    *EvalCase        `json:"case,omitempty"` // Frozen input and scoring contract for this result.
	CaseID  string           `json:"case_id"`
	Passed  bool             `json:"passed"`
	Score   float64          `json:"score"`
	Output  json.RawMessage  `json:"output"`
	Reasons []string         `json:"reasons,omitempty"`
}

type Evalset struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	SkillRef    string     `json:"skill_ref,omitempty"`
	Cases       []EvalCase `json:"cases"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type EvalRun struct {
	ID        string           `json:"id"`
	EvalsetID string           `json:"evalset_id"`
	Target    string           `json:"target"`
	Engine    string           `json:"engine"`
	Model     string           `json:"model,omitempty"`
	StartedAt time.Time        `json:"started_at"`
	EndedAt   time.Time        `json:"ended_at,omitempty"`
	Results   []EvalCaseResult `json:"results"`
	PassRate  float64          `json:"pass_rate"`
	MeanScore float64          `json:"mean_score"`
	Status    string           `json:"status"`
}

func (s *Store) UpsertEvalset(ctx context.Context, e Evalset) error {
	casesJSON, err := json.Marshal(e.Cases)
	if err != nil {
		return fmt.Errorf("marshal cases: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO evalsets (id, name, description, skill_ref, cases, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,now(),now())
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name,
			description=EXCLUDED.description,
			skill_ref=EXCLUDED.skill_ref,
			cases=EXCLUDED.cases,
			updated_at=now()`,
		e.ID, e.Name, e.Description, e.SkillRef, casesJSON)
	return err
}

func (s *Store) GetEvalset(ctx context.Context, id string) (Evalset, error) {
	var e Evalset
	var casesJSON []byte
	var desc, skillRef *string
	err := s.Pool.QueryRow(ctx, `
		SELECT id, name, description, skill_ref, cases, created_at, updated_at
		FROM evalsets WHERE id=$1`, id).Scan(
		&e.ID, &e.Name, &desc, &skillRef, &casesJSON, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	e.Description = deref(desc)
	e.SkillRef = deref(skillRef)
	if err := json.Unmarshal(casesJSON, &e.Cases); err != nil {
		return e, fmt.Errorf("unmarshal cases: %w", err)
	}
	return e, nil
}

func (s *Store) ListEvalsets(ctx context.Context) ([]Evalset, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, description, skill_ref, cases, created_at, updated_at
		FROM evalsets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evalset
	for rows.Next() {
		var e Evalset
		var casesJSON []byte
		var desc, skillRef *string
		if err := rows.Scan(&e.ID, &e.Name, &desc, &skillRef, &casesJSON, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Description = deref(desc)
		e.SkillRef = deref(skillRef)
		if err := json.Unmarshal(casesJSON, &e.Cases); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CreateEvalRun(ctx context.Context, r EvalRun) error {
	resultsJSON, err := json.Marshal(r.Results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO eval_runs (id, evalset_id, target, engine, model, started_at, results, status)
		VALUES ($1,$2,$3,$4,$5,now(),$6,$7)`,
		r.ID, r.EvalsetID, r.Target, r.Engine, r.Model, resultsJSON, r.Status)
	return err
}

func (s *Store) UpdateEvalRun(ctx context.Context, r EvalRun) error {
	resultsJSON, err := json.Marshal(r.Results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `
		UPDATE eval_runs SET
			ended_at=$1, results=$2, pass_rate=$3, mean_score=$4, status=$5
		WHERE id=$6`,
		r.EndedAt, resultsJSON, r.PassRate, r.MeanScore, r.Status, r.ID)
	return err
}

func (s *Store) GetEvalRun(ctx context.Context, id string) (EvalRun, error) {
	var r EvalRun
	var resultsJSON []byte
	var model *string
	var endedAt *time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT id, evalset_id, target, engine, model, started_at, ended_at,
		       results, pass_rate, mean_score, status
		FROM eval_runs WHERE id=$1`, id).Scan(
		&r.ID, &r.EvalsetID, &r.Target, &r.Engine, &model,
		&r.StartedAt, &endedAt, &resultsJSON, &r.PassRate, &r.MeanScore, &r.Status)
	if err != nil {
		return r, err
	}
	r.Model = deref(model)
	if endedAt != nil {
		r.EndedAt = *endedAt
	}
	if err := json.Unmarshal(resultsJSON, &r.Results); err != nil {
		return r, fmt.Errorf("unmarshal results: %w", err)
	}
	return r, nil
}

func (s *Store) ListEvalRuns(ctx context.Context, evalsetID string) ([]EvalRun, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, evalset_id, target, engine, model, started_at, ended_at,
		       results, pass_rate, mean_score, status
		FROM eval_runs WHERE evalset_id=$1 ORDER BY started_at DESC`, evalsetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalRun
	for rows.Next() {
		var r EvalRun
		var resultsJSON []byte
		var model *string
		var endedAt *time.Time
		if err := rows.Scan(&r.ID, &r.EvalsetID, &r.Target, &r.Engine, &model,
			&r.StartedAt, &endedAt, &resultsJSON, &r.PassRate, &r.MeanScore, &r.Status); err != nil {
			return nil, err
		}
		r.Model = deref(model)
		if endedAt != nil {
			r.EndedAt = *endedAt
		}
		if err := json.Unmarshal(resultsJSON, &r.Results); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
