package codex

import (
	"context"
	"testing"
	"time"
)

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name      string
		stdout    string
		wantIn    int
		wantCache int
		wantOut   int
		wantReas  int
	}{
		{
			name: "standard turn.completed event",
			stdout: `{"type":"message.delta","content":"hello"}
{"type":"turn.completed","usage":{"input_tokens":22735,"cached_input_tokens":4992,"output_tokens":6,"reasoning_output_tokens":0}}
`,
			wantIn: 22735, wantCache: 4992, wantOut: 6, wantReas: 0,
		},
		{
			name: "picks last turn.completed when multiple",
			stdout: `{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0}}
{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":50,"output_tokens":20,"reasoning_output_tokens":5}}
`,
			wantIn: 200, wantCache: 50, wantOut: 20, wantReas: 5,
		},
		{
			name:   "no turn.completed returns zeros",
			stdout: `{"type":"message.delta","content":"hello"}`,
		},
		{
			name:   "empty stdout returns zeros",
			stdout: "",
		},
		{
			name:      "with reasoning tokens",
			stdout:    `{"type":"turn.completed","usage":{"input_tokens":5000,"cached_input_tokens":1000,"output_tokens":300,"reasoning_output_tokens":150}}`,
			wantIn:    5000,
			wantCache: 1000,
			wantOut:   300,
			wantReas:  150,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, cached, out, reas := parseUsage(c.stdout)
			if in != c.wantIn {
				t.Errorf("inputTokens: want %d, got %d", c.wantIn, in)
			}
			if cached != c.wantCache {
				t.Errorf("cachedInputTokens: want %d, got %d", c.wantCache, cached)
			}
			if out != c.wantOut {
				t.Errorf("outputTokens: want %d, got %d", c.wantOut, out)
			}
			if reas != c.wantReas {
				t.Errorf("reasoningTokens: want %d, got %d", c.wantReas, reas)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Real codex / API rate-limit errors → true.
		{"ERROR: You've hit your usage limit. Visit https://chatgpt.com/codex/settings/usage", true},
		{"Usage limit reached, try again later", true},
		{"HTTP 429 Too Many Requests", true},
		{"rate limit exceeded for this account", true},
		// Normal generated content that merely contains '429' or 'rate' → false.
		// (This is the regression: a bare "429" used to false-positive and kill the node.)
		{"let accent = Color(hex: 0x4290FF) // line 429", false},
		{"the app shows a rate of 429 requests on the dashboard", false},
		{"BUILD SUCCEEDED with 0 errors", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isRateLimited(c.in); got != c.want {
			t.Errorf("isRateLimited(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCancellationStopsProcessTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := ExecAsync(ctx, "/bin/sh", []string{"-c", "sleep 2 & wait"}, ExecOpts{})
	if res.Status == 0 {
		t.Fatal("cancelled command passed")
	}
	if time.Since(start) > time.Second {
		t.Fatal("child process survived cancellation and held output pipes open")
	}
}
