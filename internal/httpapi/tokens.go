package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// NodeTokens keeps the delivery-level usage shape consumed by run clients.
type NodeTokens struct {
	Node              string  `json:"node"`
	Calls             int     `json:"calls"`
	InputTokens       int     `json:"inputTokens"`
	CachedInputTokens int     `json:"cachedInputTokens"`
	OutputTokens      int     `json:"outputTokens"`
	CacheHitRate      float64 `json:"cacheHitRate"`
}

// RunTokens is returned by GET /api/runs/{id}/tokens.
type RunTokens struct {
	Calls             int          `json:"calls"`
	InputTokens       int          `json:"inputTokens"`
	CachedInputTokens int          `json:"cachedInputTokens"`
	OutputTokens      int          `json:"outputTokens"`
	ReasoningTokens   int          `json:"reasoningTokens"`
	CacheHitRate      float64      `json:"cacheHitRate"` // cachedInputTokens / inputTokens, or 0
	ByNode            []NodeTokens `json:"byNode"`
}

// RunTokensSummary reports new usage from one execution, excluding any
// cumulative usage carried into a continuation.
type RunTokensSummary struct {
	RunID             string `json:"runId"`
	DAG               string `json:"dag"`
	StartedAt         string `json:"startedAt"`
	InputTokens       int    `json:"inputTokens"`
	CachedInputTokens int    `json:"cachedInputTokens"`
	OutputTokens      int    `json:"outputTokens"`
}

// StatsTokens is returned by GET /api/stats/tokens.
type StatsTokens struct {
	TotalCalls        int                `json:"totalCalls"`
	InputTokens       int                `json:"inputTokens"`
	CachedInputTokens int                `json:"cachedInputTokens"`
	OutputTokens      int                `json:"outputTokens"`
	ReasoningTokens   int                `json:"reasoningTokens"`
	CacheHitRate      float64            `json:"cacheHitRate"`
	ByRun             []RunTokensSummary `json:"byRun"`
}

// SDK usage comes from the committed checkpoint, including completed turns
// before a later failure. No filesystem sidecar is another source of truth.
type sdkTokenCheckpoint struct {
	Attempts int  `json:"attempts"`
	Attempt  *int `json:"attempt"`
	Usage    struct {
		Input  int `json:"input_tokens"`
		Cached int `json:"cached_input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
}

func (checkpoint sdkTokenCheckpoint) tokens() RunTokens {
	// Calls counts started attempts, including a current or failed turn; usage
	// only includes tokens actually reported by completed turns.
	calls := checkpoint.Attempts
	if checkpoint.Attempt != nil && calls < *checkpoint.Attempt+1 {
		calls = *checkpoint.Attempt + 1
	}
	return tokenTotals(calls, checkpoint.Usage.Input, checkpoint.Usage.Cached, checkpoint.Usage.Output)
}

func tokenTotals(calls, input, cached, output int) RunTokens {
	rt := RunTokens{Calls: calls, InputTokens: input, CachedInputTokens: cached, OutputTokens: output}
	if rt.InputTokens > 0 {
		rt.CacheHitRate = float64(rt.CachedInputTokens) / float64(rt.InputTokens)
	}
	rt.ByNode = []NodeTokens{{Node: "delivery", Calls: calls, InputTokens: rt.InputTokens, CachedInputTokens: rt.CachedInputTokens, OutputTokens: rt.OutputTokens, CacheHitRate: rt.CacheHitRate}}
	return rt
}

// Individual runs retain cumulative totals for their workflow budget display.
func sdkRunTokens(run store.Run) RunTokens {
	var doc struct {
		SDK sdkTokenCheckpoint `json:"sdk"`
	}
	_ = json.Unmarshal(run.Doc, &doc)
	return doc.SDK.tokens()
}

// Aggregate each execution's newly reported usage, not its inherited budget
// total. The launch seed is immutable and remains available if the source run
// is deleted. Missing or not-yet-committed usage cannot produce negative totals.
func sdkExecutionTokens(run store.Run) RunTokens {
	total := sdkRunTokens(run)
	var launch struct {
		Request struct {
			Continuation struct {
				Checkpoint sdkTokenCheckpoint `json:"checkpoint"`
			} `json:"continuation"`
		} `json:"request"`
	}
	_ = json.Unmarshal(run.Options, &launch)
	seed := launch.Request.Continuation.Checkpoint.tokens()
	return tokenTotals(max(0, total.Calls-seed.Calls), max(0, total.InputTokens-seed.InputTokens),
		max(0, total.CachedInputTokens-seed.CachedInputTokens), max(0, total.OutputTokens-seed.OutputTokens))
}

// handleRunTokens handles GET /api/runs/{id}/tokens.
func (s *Server) handleRunTokens(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	run, err := s.store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	var launch struct {
		Runtime string `json:"runtime"`
	}
	_ = json.Unmarshal(run.Options, &launch)
	if launch.Runtime != "codex-sdk" {
		writeJSON(w, http.StatusConflict, errBody(fmt.Errorf("token usage is available for SDK runs only")))
		return
	}
	rt := sdkRunTokens(run)
	writeJSON(w, http.StatusOK, rt)
}

// handleStatsTokens aggregates committed SDK checkpoints. Artifact directories
// and model-writable sidecars are not telemetry inputs.
func (s *Server) handleStatsTokens(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(r.Context(), store.RunFilter{Runtime: "codex-sdk"})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// Chart chronology comes from stored launch time, not a run ID convention
	// or the time an older run happened to receive another checkpoint.
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}
		return runs[i].RunID > runs[j].RunID
	})
	stats := StatsTokens{ByRun: []RunTokensSummary{}}
	const byRunLimit = 15
	for _, run := range runs {
		usage := sdkExecutionTokens(run)
		stats.TotalCalls += usage.Calls
		stats.InputTokens += usage.InputTokens
		stats.CachedInputTokens += usage.CachedInputTokens
		stats.OutputTokens += usage.OutputTokens
		if len(stats.ByRun) < byRunLimit {
			started := ""
			if !run.StartedAt.IsZero() {
				started = run.StartedAt.UTC().Format(time.RFC3339Nano)
			}
			stats.ByRun = append(stats.ByRun, RunTokensSummary{RunID: run.RunID, DAG: run.DAG, StartedAt: started,
				InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens})
		}
	}
	if stats.InputTokens > 0 {
		stats.CacheHitRate = float64(stats.CachedInputTokens) / float64(stats.InputTokens)
	}
	// Newest belongs on the right side of the chart.
	for i, j := 0, len(stats.ByRun)-1; i < j; i, j = i+1, j-1 {
		stats.ByRun[i], stats.ByRun[j] = stats.ByRun[j], stats.ByRun[i]
	}
	writeJSON(w, http.StatusOK, stats)
}
