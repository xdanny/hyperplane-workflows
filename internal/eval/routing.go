package eval

import (
	"context"
	"errors"
	"fmt"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"math"
	"strings"
	"time"
)

var ErrInvalidRoutingEvaluation = errors.New("invalid routing evaluation")
var ErrNoPassingRoutingEngine = errors.New("neither engine passed a case; routing policy unchanged")

type RoutingEvaluation struct {
	Skill    string              `json:"skill"`
	Decision string              `json:"decision"`
	Compare  CompareResult       `json:"compare"`
	Policy   store.RoutingPolicy `json:"policy"`
}

// EvaluateRouting is the shared CLI/API operation. Both engines receive the same
// resolved instructions, frozen cases and judge; a policy commits with its evidence.
func EvaluateRouting(ctx context.Context, st *store.Store, skillID, evalsetID string, margin float64, cwd string) (RoutingEvaluation, error) {
	result := RoutingEvaluation{Skill: skillID}
	if strings.TrimSpace(skillID) == "" || strings.TrimSpace(evalsetID) == "" || math.IsNaN(margin) || math.IsInf(margin, 0) || margin < 0 || margin > 1 {
		return result, fmt.Errorf("%w: skill and evalset are required; margin must be between 0 and 1", ErrInvalidRoutingEvaluation)
	}
	es, err := st.GetEvalset(ctx, evalsetID)
	if err != nil {
		return result, fmt.Errorf("get evalset: %w", err)
	}
	skill, err := st.GetSkill(ctx, skillID)
	if err != nil {
		return result, fmt.Errorf("get skill: %w", err)
	}
	var judge Judge
	for _, c := range es.Cases {
		if strings.TrimSpace(c.Criteria) != "" {
			judge = &CodexJudge{CWD: cwd}
			break
		}
	}
	cfg := Config{EvalsetID: es.ID, Target: skill.Instruction, Engine: &LocalEngine{}, Judge: judge}
	if err := Validate(es, cfg); err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidRoutingEvaluation, err)
	}
	persistDiagnostics := func(runs ...store.EvalRun) error {
		for _, run := range runs {
			if err := st.SaveEvalRun(ctx, run); err != nil {
				return fmt.Errorf("save evaluation diagnostics: %w", err)
			}
		}
		return nil
	}
	localRun, err := RunInMemory(ctx, es, cfg)
	localRun.ID = fmt.Sprintf("er-local-%d", time.Now().UnixNano())
	if err != nil {
		return result, errors.Join(fmt.Errorf("local eval: %w", err), persistDiagnostics(localRun))
	}
	cfg.Engine = &CodexEngine{CWD: cwd}
	codexRun, err := RunInMemory(ctx, es, cfg)
	codexRun.ID = fmt.Sprintf("er-codex-%d", time.Now().UnixNano())
	if err != nil {
		return result, errors.Join(fmt.Errorf("codex eval: %w", err), persistDiagnostics(localRun, codexRun))
	}
	comparison, err := CompareRuns(localRun, codexRun)
	if err != nil {
		return result, err
	}
	result.Compare = comparison
	if localRun.PassRate == 0 && codexRun.PassRate == 0 {
		return result, errors.Join(ErrNoPassingRoutingEngine, persistDiagnostics(localRun, codexRun))
	}
	result.Decision = "codex"
	confidence := codexRun.PassRate
	if localRun.PassRate > 0 && localRun.PassRate >= codexRun.PassRate-margin {
		result.Decision = "codex-local"
		confidence = localRun.PassRate
	}
	result.Policy = store.RoutingPolicy{Scope: "skill:" + skillID, Engine: result.Decision, Confidence: &confidence, Margin: &margin, EvalRunA: localRun.ID, EvalRunB: codexRun.ID, Note: fmt.Sprintf("local=%.2f codex=%.2f margin=%.2f winner=%s", localRun.PassRate, codexRun.PassRate, margin, comparison.Winner)}
	if err := st.SaveRoutingEvaluation(ctx, result.Policy, es, skill.Instruction, localRun, codexRun); err != nil {
		return result, err
	}
	return result, nil
}
