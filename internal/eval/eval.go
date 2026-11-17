package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Engine is the pluggable execution backend.
type Engine interface {
	Run(ctx context.Context, instruction string, input json.RawMessage) (json.RawMessage, error)
}

// Judge is optional LLM-as-judge for criteria scoring.
type Judge interface {
	Score(ctx context.Context, criterion string, output json.RawMessage) (passed bool, score float64, reasons []string, err error)
}

// Config controls a single eval run.
type Config struct {
	EvalsetID string
	Target    string // instruction text; Run resolves skill:<id> from storage
	Engine    Engine
	Judge     Judge  // optional; nil = deterministic-only
	RunDir    string // for temp files (can be "")
}

// scoreChecks runs deterministic checks against output.
func scoreChecks(output json.RawMessage, checks []store.EvalCheckSpec) (passed bool, reasons []string) {
	if len(checks) == 0 {
		return true, nil
	}
	allPassed := true
	for _, chk := range checks {
		switch chk.Kind {
		case "json-parse":
			var v interface{}
			if err := json.Unmarshal(output, &v); err != nil {
				allPassed = false
				reasons = append(reasons, fmt.Sprintf("json-parse: invalid JSON: %v", err))
			} else {
				reasons = append(reasons, "json-parse: ok")
			}
		case "text-nonempty":
			s := strings.TrimSpace(string(output))
			var text string
			if json.Unmarshal(output, &text) == nil {
				s = strings.TrimSpace(text)
			}
			if s == "" || s == "null" {
				allPassed = false
				reasons = append(reasons, "text-nonempty: output is empty")
			} else {
				reasons = append(reasons, "text-nonempty: ok")
			}
		case "count-min":
			if chk.Min != nil {
				var arr []interface{}
				if err := json.Unmarshal(output, &arr); err != nil || !strings.HasPrefix(strings.TrimSpace(string(output)), "[") {
					allPassed = false
					reasons = append(reasons, fmt.Sprintf("count-min: cannot parse as array: %v", err))
				} else if len(arr) < *chk.Min {
					allPassed = false
					reasons = append(reasons, fmt.Sprintf("count-min: got %d, need >= %d", len(arr), *chk.Min))
				} else {
					reasons = append(reasons, fmt.Sprintf("count-min: %d >= %d ok", len(arr), *chk.Min))
				}
			}
		default:
			allPassed = false
			reasons = append(reasons, fmt.Sprintf("unsupported check kind: %s", chk.Kind))
		}
	}
	return allPassed, reasons
}

// Validate requires an actual scoring contract before any engine calls.
func Validate(es store.Evalset, cfg Config) error {
	if cfg.Engine == nil || strings.TrimSpace(cfg.Target) == "" {
		return fmt.Errorf("an engine and resolved target instructions are required")
	}
	if len(es.Cases) == 0 {
		return fmt.Errorf("evalset must contain at least one scored case")
	}
	seen := map[string]bool{}
	for _, c := range es.Cases {
		if strings.TrimSpace(c.ID) == "" || seen[c.ID] {
			return fmt.Errorf("eval cases need unique nonempty ids")
		}
		seen[c.ID] = true
		if len(c.Input) > 0 && !json.Valid(c.Input) {
			return fmt.Errorf("case %s has invalid JSON input", c.ID)
		}
		if len(c.Expect) > 0 {
			return fmt.Errorf("case %s uses unsupported expect; declare a check or judge criterion", c.ID)
		}
		if len(c.Checks) == 0 && strings.TrimSpace(c.Criteria) == "" {
			return fmt.Errorf("case %s has no scoring checks or criteria", c.ID)
		}
		if strings.TrimSpace(c.Criteria) != "" && cfg.Judge == nil {
			return fmt.Errorf("case %s requires a judge for its criteria", c.ID)
		}
		for _, check := range c.Checks {
			if check.Key != "" || check.Max != nil {
				return fmt.Errorf("case %s has unsupported check options", c.ID)
			}
			switch check.Kind {
			case "json-parse", "text-nonempty":
				if check.Min != nil {
					return fmt.Errorf("case %s has unsupported minimum for %s", c.ID, check.Kind)
				}
			case "count-min":
				if check.Min == nil || *check.Min < 0 {
					return fmt.Errorf("case %s count-min needs a nonnegative minimum", c.ID)
				}
			default:
				return fmt.Errorf("case %s has unsupported check kind %q", c.ID, check.Kind)
			}
		}
	}
	return nil
}

// RunInMemory stops on execution/scoring errors. Partial case diagnostics remain,
// but an incomplete evaluation has no aggregate quality score and cannot compare.
func RunInMemory(ctx context.Context, es store.Evalset, cfg Config) (store.EvalRun, error) {
	run := store.EvalRun{EvalsetID: es.ID, Target: cfg.Target, Engine: "custom", StartedAt: time.Now(), Status: "running", Results: []store.EvalCaseResult{}}
	switch engine := cfg.Engine.(type) {
	case *MockEngine:
		run.Engine = "mock"
	case *LocalEngine:
		run.Engine = "local"
		run.Model = engine.Model
	case *CodexEngine:
		run.Engine = "codex"
		run.Model = engine.Model
	}
	fail := func(id string, output json.RawMessage, err error) (store.EvalRun, error) {
		if id != "" {
			if !json.Valid(output) {
				output = json.RawMessage(`null`)
			}
			var snapshot *store.EvalCase
			for _, c := range es.Cases {
				if c.ID == id {
					snapshot = &c
					break
				}
			}
			run.Results = append(run.Results, store.EvalCaseResult{Case: snapshot, CaseID: id, Output: output, Reasons: []string{err.Error()}})
		}
		run.Status = "failed"
		run.EndedAt = time.Now()
		run.PassRate = 0
		run.MeanScore = 0
		return run, err
	}
	if err := Validate(es, cfg); err != nil {
		return fail("", nil, err)
	}
	judgeConfig := judgeConfiguration(cfg.Judge)
	var totalScore float64
	var passCount int
	for _, c := range es.Cases {
		if err := ctx.Err(); err != nil {
			return fail(c.ID, nil, err)
		}
		output, err := cfg.Engine.Run(ctx, cfg.Target, c.Input)
		if err != nil {
			return fail(c.ID, output, fmt.Errorf("case %s engine: %w", c.ID, err))
		}
		if err := ctx.Err(); err != nil {
			return fail(c.ID, output, err)
		}
		checkPassed, checkReasons := scoreChecks(output, c.Checks)
		judgePassed, judgeScore := true, 1.0
		var judgeReasons []string
		if strings.TrimSpace(c.Criteria) != "" {
			judgePassed, judgeScore, judgeReasons, err = cfg.Judge.Score(ctx, c.Criteria, output)
			if err != nil {
				return fail(c.ID, output, fmt.Errorf("case %s judge: %w", c.ID, err))
			}
			if math.IsNaN(judgeScore) || math.IsInf(judgeScore, 0) || judgeScore < 0 || judgeScore > 1 {
				return fail(c.ID, output, fmt.Errorf("case %s judge returned an invalid score", c.ID))
			}
		}
		if err := ctx.Err(); err != nil {
			return fail(c.ID, output, err)
		}
		if !json.Valid(output) {
			output, _ = json.Marshal(string(output))
		}
		score := judgeScore
		if !checkPassed {
			score = 0
		}
		passed := checkPassed && judgePassed
		if passed {
			passCount++
		}
		totalScore += score
		var scorer *store.EvalJudgeConfig
		if strings.TrimSpace(c.Criteria) != "" && judgeConfig != nil {
			copy := *judgeConfig
			scorer = &copy
		}
		run.Results = append(run.Results, store.EvalCaseResult{Judge: scorer, Case: &c, CaseID: c.ID, Passed: passed, Score: score, Output: output, Reasons: append(checkReasons, judgeReasons...)})
	}
	run.PassRate = float64(passCount) / float64(len(es.Cases))
	run.MeanScore = totalScore / float64(len(es.Cases))
	run.Status = "done"
	run.EndedAt = time.Now()
	return run, nil
}

// Run executes all cases in the evalset and persists the run.
func Run(ctx context.Context, st *store.Store, cfg Config) (store.EvalRun, error) {
	es, err := st.GetEvalset(ctx, cfg.EvalsetID)
	if err != nil {
		return store.EvalRun{}, fmt.Errorf("get evalset: %w", err)
	}

	if strings.TrimSpace(cfg.Target) == "" {
		if es.SkillRef == "" {
			return store.EvalRun{}, fmt.Errorf("target instructions or an evalset skill reference are required")
		}
		cfg.Target = "skill:" + es.SkillRef
	}
	if strings.HasPrefix(cfg.Target, "skill:") {
		skill, err := st.GetSkill(ctx, strings.TrimPrefix(cfg.Target, "skill:"))
		if err != nil {
			return store.EvalRun{}, fmt.Errorf("resolve skill target: %w", err)
		}
		cfg.Target = skill.Instruction
	}
	run, executionErr := RunInMemory(ctx, es, cfg)
	run.ID = fmt.Sprintf("er-%d", time.Now().UnixNano())

	if err := st.SaveEvalRun(ctx, run); err != nil {
		return run, errors.Join(executionErr, fmt.Errorf("save eval run: %w", err))
	}
	return run, executionErr
}

// CompareResult summarizes two eval runs side by side.
type CompareResult struct {
	RunAID        string
	RunBID        string
	Winner        string // "A", "B", "tie"
	PassRateDiff  float64
	MeanScoreDiff float64
	PerCase       []CaseComparison
}

// CaseComparison holds per-case comparison data.
type CaseComparison struct {
	CaseID  string
	PassedA bool
	PassedB bool
	ScoreA  float64
	ScoreB  float64
}

// CompareRuns returns a summary comparing two eval runs side by side.
func CompareRuns(runA, runB store.EvalRun) (CompareResult, error) {
	invalid := func() (CompareResult, error) {
		return CompareResult{}, fmt.Errorf("comparison requires completed evaluations of the same frozen cases and scoring contract")
	}
	if runA.EvalsetID != runB.EvalsetID || len(runA.Results) == 0 || len(runA.Results) != len(runB.Results) {
		return invalid()
	}
	snapshots := map[string]*store.EvalCase{}
	scorers := map[string]*store.EvalJudgeConfig{}
	for _, run := range []store.EvalRun{runA, runB} {
		if run.Status != "done" || run.Target == "" {
			return invalid()
		}
		seen := map[string]bool{}
		passCount := 0
		total := 0.0
		for _, result := range run.Results {
			if result.Case == nil || result.CaseID == "" || result.Case.ID != result.CaseID || seen[result.CaseID] || math.IsNaN(result.Score) || math.IsInf(result.Score, 0) || result.Score < 0 || result.Score > 1 {
				return invalid()
			}
			if strings.TrimSpace(result.Case.Criteria) != "" {
				if !validJudgeConfiguration(result.Judge) {
					return invalid()
				}
				if prior, ok := scorers[result.CaseID]; ok {
					if !reflect.DeepEqual(prior, result.Judge) {
						return invalid()
					}
				} else {
					scorers[result.CaseID] = result.Judge
				}
			} else if result.Judge != nil {
				return invalid()
			}
			seen[result.CaseID] = true
			total += result.Score
			if result.Passed {
				passCount++
			}
			if prior, ok := snapshots[result.CaseID]; ok {
				if !reflect.DeepEqual(prior, result.Case) {
					return invalid()
				}
			} else {
				snapshots[result.CaseID] = result.Case
			}
		}
		if math.IsNaN(run.PassRate) || math.IsNaN(run.MeanScore) || math.Abs(run.PassRate-float64(passCount)/float64(len(run.Results))) > 1e-9 || math.Abs(run.MeanScore-total/float64(len(run.Results))) > 1e-9 {
			return invalid()
		}
	}
	if len(snapshots) != len(runA.Results) {
		return invalid()
	}
	res := CompareResult{
		RunAID:        runA.ID,
		RunBID:        runB.ID,
		PassRateDiff:  runA.PassRate - runB.PassRate,
		MeanScoreDiff: runA.MeanScore - runB.MeanScore,
	}

	byID := make(map[string]store.EvalCaseResult, len(runB.Results))
	for _, result := range runB.Results {
		byID[result.CaseID] = result
	}
	for _, a := range runA.Results {
		b := byID[a.CaseID]
		res.PerCase = append(res.PerCase, CaseComparison{CaseID: a.CaseID, PassedA: a.Passed, PassedB: b.Passed, ScoreA: a.Score, ScoreB: b.Score})
	}

	if res.PassRateDiff > 0 {
		res.Winner = "A"
	} else if res.PassRateDiff < 0 {
		res.Winner = "B"
	} else if res.MeanScoreDiff > 0 {
		res.Winner = "A"
	} else if res.MeanScoreDiff < 0 {
		res.Winner = "B"
	} else {
		res.Winner = "tie"
	}

	return res, nil
}
