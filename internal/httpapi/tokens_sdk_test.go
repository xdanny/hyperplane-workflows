package httpapi

import (
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKRunTokensUsesCumulativeCheckpointOnce(t *testing.T) {
	for _, tc := range []struct {
		name, doc                    string
		calls, input, cached, output int
	}{
		{"starting", `{"sdk":{"phase":"starting"}}`, 0, 0, 0, 0},
		{"first_turn_in_flight", `{"sdk":{"phase":"running","attempt":0}}`, 1, 0, 0, 0},
		{"correction_in_flight", `{"sdk":{"phase":"running","attempt":1,"usage":{"input_tokens":40,"cached_input_tokens":10,"output_tokens":8}}}`, 2, 40, 10, 8},
		{"correction_transport_failed", `{"sdk":{"phase":"failed","attempts":2,"attempt":1,"usage":{"input_tokens":40,"cached_input_tokens":10,"output_tokens":8}}}`, 2, 40, 10, 8},
		{"node_totals_already_aggregated", `{"sdk":{"phase":"passed","attempts":3,"usage":{"input_tokens":120,"cached_input_tokens":30,"output_tokens":24},"nodes":[{"id":"one","attempts":1,"usage":{"input_tokens":40}},{"id":"two","attempts":2,"usage":{"input_tokens":80}}]}}`, 3, 120, 30, 24},
		{"approval_wait", `{"sdk":{"phase":"approval_pending","attempts":1,"usage":{"input_tokens":40,"cached_input_tokens":10,"output_tokens":8}}}`, 1, 40, 10, 8},
		{"no_checkpoint", `{}`, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sdkRunTokens(store.Run{Doc: store.JSON(tc.doc), Summary: store.JSON(`{"usage":{"input_tokens":99999}}`)})
			if got.Calls != tc.calls || got.InputTokens != tc.input || got.CachedInputTokens != tc.cached || got.OutputTokens != tc.output || got.ReasoningTokens != 0 {
				t.Fatalf("checkpoint aggregation: %+v", got)
			}
			wantRate := 0.0
			if tc.input > 0 {
				wantRate = float64(tc.cached) / float64(tc.input)
			}
			if got.CacheHitRate != wantRate || len(got.ByNode) != 1 || got.ByNode[0].Node != "delivery" || got.ByNode[0].Calls != tc.calls || got.ByNode[0].InputTokens != tc.input || got.ByNode[0].CachedInputTokens != tc.cached || got.ByNode[0].OutputTokens != tc.output || got.ByNode[0].CacheHitRate != wantRate {
				t.Fatalf("delivery grouping/cache rate: %+v", got)
			}
		})
	}
}

func TestSDKExecutionTokensSubtractsOnlyCommittedLaunchSeed(t *testing.T) {
	for _, tc := range []struct {
		name, doc, options           string
		calls, input, cached, output int
	}{
		{"ordinary", `{"sdk":{"attempts":3,"usage":{"input_tokens":120,"cached_input_tokens":30,"output_tokens":24}}}`, `{"runtime":"codex-sdk"}`, 3, 120, 30, 24},
		{"successor", `{"sdk":{"attempts":3,"usage":{"input_tokens":120,"cached_input_tokens":30,"output_tokens":24}}}`, `{"request":{"continuation":{"checkpoint":{"attempts":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}}}`, 1, 20, 5, 4},
		{"renewed_approval_no_new_turn", `{"sdk":{"attempts":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}`, `{"request":{"continuation":{"checkpoint":{"attempts":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}}}`, 0, 0, 0, 0},
		{"new_turn_started_usage_pending", `{"sdk":{"attempts":2,"attempt":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}`, `{"request":{"continuation":{"checkpoint":{"attempts":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}}}`, 1, 0, 0, 0},
		{"missing_receipt_cannot_subtract_others", `{}`, `{"request":{"continuation":{"checkpoint":{"attempts":2,"usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":20}}}}}`, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sdkExecutionTokens(store.Run{Doc: store.JSON(tc.doc), Options: store.JSON(tc.options)})
			if got.Calls != tc.calls || got.InputTokens != tc.input || got.CachedInputTokens != tc.cached || got.OutputTokens != tc.output {
				t.Fatalf("execution usage: %+v", got)
			}
			wantRate := 0.0
			if tc.input > 0 {
				wantRate = float64(tc.cached) / float64(tc.input)
			}
			if got.CacheHitRate != wantRate || got.ByNode[0].InputTokens != tc.input || got.ByNode[0].Calls != tc.calls {
				t.Fatalf("execution grouping/rate: %+v", got)
			}
		})
	}
}
