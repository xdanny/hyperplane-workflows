package eval

import (
	"encoding/json"
	"testing"
)

func TestMalformedJudgeResponseIsExecutionError(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"score":2,"reasons":["reason"]}`, `{"score":0.9}`, `{"score":0.9,"reasons":[""]}`, `{"score":"0.9","reasons":["reason"]}`} {
		pass, score, _, err := parseJudgeResponse(json.RawMessage(raw), "")
		if err == nil || pass || score != 0 {
			t.Fatalf("malformed judge certified: %s", raw)
		}
	}
	pass, score, _, err := parseJudgeResponse(json.RawMessage(`{"score":0.9,"pass":false,"reasons":["specific weakness"]}`), "")
	if err != nil || !pass || score != .9 {
		t.Fatalf("valid judgment lost: %v", err)
	}
}

func TestJudgeConfigurationPreservesSelectionWithoutCredentials(t *testing.T) {
	t.Setenv("LOCAL_LLM_MODEL", "configured-judge")
	t.Setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:12345/v1")
	local := judgeConfiguration(&LocalJudge{})
	if !validJudgeConfiguration(local) || local.Model != "configured-judge" || local.ModelDefault || local.Provider != "local" {
		t.Fatalf("local identity missing: %+v", local)
	}
	codex := judgeConfiguration(&CodexJudge{})
	if !validJudgeConfiguration(codex) || !codex.ModelDefault || codex.Model != "" || codex.Provider != "codex" {
		t.Fatalf("default selection falsely pinned: %+v", codex)
	}
	explicit := judgeConfiguration(&CodexJudge{Model: "explicit-judge"})
	if !validJudgeConfiguration(explicit) || explicit.ModelDefault || explicit.Model != "explicit-judge" {
		t.Fatal("explicit model identity missing")
	}
	t.Setenv("LOCAL_LLM_BASE_URL", "http://user:credential@127.0.0.1:12345/v1")
	if judgeConfiguration(&LocalJudge{}) != nil {
		t.Fatal("credential-bearing endpoint became comparison evidence")
	}
	t.Setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:12345/v1?key=credential")
	if judgeConfiguration(&LocalJudge{}) != nil {
		t.Fatal("query credentials became comparison evidence")
	}
}
