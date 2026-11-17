package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// judgePassThreshold is the authoritative pass bar: a refinement passes only if the
// judge scores it SOLID-or-better. Computed in Go (not trusting the model's own
// "pass" flag) so the bar is consistent. Raised from 0.7 → 0.85 once the harsh rubric
// made everything-passes-at-0.7 too lenient to discriminate.
const judgePassThreshold = 0.85

// judgePrompt builds the strict evaluation prompt sent to the judge model.
// The response must be a JSON object: {"score":float,"pass":bool,"reasons":[...]}.
func judgePrompt(criterion string, output json.RawMessage) string {
	return fmt.Sprintf(`You are a HARSH, discriminating evaluation judge. Score 0.0–1.0 how well OUTPUT satisfies CRITERION — judge the QUALITY and DEPTH of satisfaction, NEVER the mere presence of the required elements. A response that just ticks the boxes is mediocre, not good.

CALIBRATION (be stingy — most real outputs are mediocre, so most scores belong in the 0.4–0.7 band):
- 0.90–1.00 EXCEPTIONAL: fully satisfies AND is specific, original, non-obvious, and concrete on every dimension. Rare.
- 0.70–0.89 SOLID: satisfies well with real specifics; only minor weaknesses.
- 0.40–0.69 MEDIOCRE: technically mentions the required elements but is generic, vague, derivative, or thin. MOST OUTPUTS BELONG HERE.
- 0.00–0.39 POOR: misses, fails, or only superficially addresses the criterion.

Penalize hard and explicitly: genericness, vagueness, clichés, derivative/obvious ideas, hand-waving, buzzwords, and any element that is named but not made concrete. Do NOT inflate a confident-sounding but shallow answer.

In "reasons", you MUST cite at least one SPECIFIC weakness that capped the score (what is generic, vague, derivative, or missing) — even for high scores.

Return ONLY {"score":float,"pass":bool,"reasons":[...]} where pass = score>=0.85 (only SOLID-or-better work passes; mediocre box-ticking fails).

CRITERION:
%s

OUTPUT:
%s`, criterion, string(output))
}

// judgeResponse is the expected JSON shape from a judge model call.
type judgeResponse struct {
	Score   *float64 `json:"score"`
	Pass    bool     `json:"pass"`
	Reasons []string `json:"reasons"`
}

// A malformed judge answer is a scoring failure, not a quality verdict.
func parseJudgeResponse(j json.RawMessage, text string) (bool, float64, []string, error) {
	if len(j) == 0 {
		text = strings.TrimSpace(text)
		start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
		if start >= 0 && end > start {
			j = json.RawMessage(text[start : end+1])
		}
	}
	var response judgeResponse
	if err := json.Unmarshal(j, &response); err != nil || response.Score == nil || *response.Score < 0 || *response.Score > 1 || len(response.Reasons) == 0 {
		return false, 0, nil, fmt.Errorf("judge returned an invalid scoring response")
	}
	for _, reason := range response.Reasons {
		if strings.TrimSpace(reason) == "" {
			return false, 0, nil, fmt.Errorf("judge returned an empty reason")
		}
	}
	return *response.Score >= judgePassThreshold, *response.Score, response.Reasons, nil
}

// CodexJudge implements Judge using the codex adapter (the billed engine).
// It mirrors CodexEngine: SandboxReadOnly, Reasoning "low", Label "eval-judge".
type CodexJudge struct {
	Model  string // empty delegates model selection to the isolated SDK default
	RunDir string
	CWD    string
}

// Score calls codex with the judge prompt and parses the structured response.
func (j *CodexJudge) Score(ctx context.Context, criterion string, output json.RawMessage) (passed bool, score float64, reasons []string, err error) {
	res, err := codex.Run(ctx, codex.Options{
		Instruction: judgePrompt(criterion, output),
		Model:       j.Model,
		Reasoning:   "low",
		Cwd:         j.CWD,
		RunDir:      j.RunDir,
		Label:       "eval-judge",
		SandboxMode: codex.SandboxReadOnly,
	})
	if err != nil {
		return false, 0, nil, err
	}
	if res.RateLimited {
		return false, 0, nil, fmt.Errorf("codex judge rate-limited")
	}
	if !res.OK {
		return false, 0, nil, fmt.Errorf("codex judge failed (status %d)", res.Status)
	}
	return parseJudgeResponse(res.JSON, res.Text)
}

// LocalJudge implements Judge using the local OpenAI-compatible model adapter.
// It mirrors LocalEngine: same base URL / model env vars apply.
type LocalJudge struct {
	Model string // empty ⇒ LOCAL_LLM_MODEL / default
}

// Score calls the local model with the judge prompt and parses the structured response.
func (j *LocalJudge) Score(ctx context.Context, criterion string, output json.RawMessage) (passed bool, score float64, reasons []string, err error) {
	res, err := localmodel.Run(ctx, localmodel.Options{
		Instruction: judgePrompt(criterion, output),
		Model:       j.Model,
	})
	if err != nil {
		return false, 0, nil, err
	}
	if !res.OK {
		return false, 0, nil, fmt.Errorf("local judge returned no usable output")
	}
	return parseJudgeResponse(res.JSON, res.Text)
}

// judgeConfiguration is deliberately optional: a custom Judge can score a case,
// but its result cannot participate in a comparison without an identified scorer.
func judgeConfiguration(j Judge) *store.EvalJudgeConfig {
	rubric := fmt.Sprintf("%s\npass-threshold=%g\nresponse-validator=1", judgePrompt("", nil), judgePassThreshold)
	fingerprint := sha256.Sum256([]byte(rubric))
	config := &store.EvalJudgeConfig{RubricFingerprint: fmt.Sprintf("%x", fingerprint)}
	switch judge := j.(type) {
	case *CodexJudge:
		if judge == nil {
			return nil
		}
		config.Provider = "codex"
		config.Model = judge.Model
		config.ModelDefault = judge.Model == ""
	case *LocalJudge:
		if judge == nil {
			return nil
		}
		config.Provider = "local"
		config.Model = judge.Model
		if config.Model == "" {
			config.Model = os.Getenv("LOCAL_LLM_MODEL")
		}
		config.ModelDefault = config.Model == ""
		endpoint := os.Getenv("LOCAL_LLM_BASE_URL")
		if endpoint == "" {
			endpoint = "provider-default"
		} else {
			// Never persist credentials or silently erase routing query settings.
			// Unidentifiable endpoints may run, but cannot justify comparison.
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return nil
			}
			endpoint = strings.TrimRight(parsed.String(), "/")
		}
		sum := sha256.Sum256([]byte(endpoint))
		config.EndpointFingerprint = fmt.Sprintf("%x", sum)
	default:
		return nil
	}
	return config
}

func validJudgeConfiguration(config *store.EvalJudgeConfig) bool {
	if config == nil || len(config.RubricFingerprint) != 64 || config.ModelDefault != (config.Model == "") {
		return false
	}
	if config.Provider == "codex" {
		return config.EndpointFingerprint == ""
	}
	return config.Provider == "local" && len(config.EndpointFingerprint) == 64
}
