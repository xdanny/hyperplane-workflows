package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/httpapi"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The database checkpoint is the sole source, even when run folders are absent.
func TestSDKTokenEndpointsAggregateCommittedReceipts(t *testing.T) {
	runsPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(runsPath, []byte("token reporting must not read this"), 0600); err != nil {
		t.Fatal(err)
	}
	_, server := newTestServer(t, func(c *config.Config) { c.RunsDir = runsPath })
	st := openStore(t)
	read := func(path string, want int, output any) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s: status=%d want=%d", path, response.StatusCode, want)
		}
		if output != nil {
			if err := json.NewDecoder(response.Body).Decode(output); err != nil {
				t.Fatal(err)
			}
		}
	}
	var before httpapi.StatsTokens
	read("/api/stats/tokens", 200, &before)
	// IDs deliberately sort opposite to launch timestamps; chart labels must use
	// stored workflow identity and timestamps, including timezone conversion.
	prefix := fmt.Sprintf("sdk-token-proof-%d", time.Now().UnixNano())
	start := time.Now().UTC().AddDate(10, 0, 0).Truncate(time.Second).In(time.FixedZone("fixture", 3*60*60))
	const records = 17
	var ids []string
	for i := 0; i < records; i++ {
		id := fmt.Sprintf("%s-%02d", prefix, records-i)
		ids = append(ids, id)
		run := store.Run{RunID: id, DAG: "node-workflow", State: "running", StartedAt: start.Add(time.Duration(i) * time.Minute), OwnerPID: os.Getpid(), Options: store.JSON(`{"runtime":"codex-sdk"}`)}
		if err := st.CreateRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), id) })
		// Repeated snapshots contain cumulative totals and must not be summed twice.
		for _, phase := range []string{"running", "failed"} {
			checkpoint, _ := json.Marshal(map[string]any{"phase": phase, "attempts": 2, "usage": map[string]int{"input_tokens": 10 * (i + 1), "cached_input_tokens": 2 * (i + 1), "output_tokens": i + 1}})
			terminal := ""
			if phase == "failed" {
				terminal = phase
			}
			if _, _, err := st.SaveSDKCheckpoint(t.Context(), id, "", os.Getpid(), checkpoint, terminal, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	var one httpapi.RunTokens
	read("/api/runs/"+ids[0]+"/tokens", 200, &one)
	if one.Calls != 2 || one.InputTokens != 10 || one.CachedInputTokens != 2 || one.OutputTokens != 1 || one.CacheHitRate != 0.2 || len(one.ByNode) != 1 || one.ByNode[0].Node != "delivery" {
		t.Fatalf("committed failed receipt lost: %+v", one)
	}
	// Neither an unrelated run record nor a missing record is a filesystem fallback.
	legacy := prefix + "-unsupported"
	if err := st.CreateRun(t.Context(), store.Run{RunID: legacy, DAG: "retired", State: "passed", Doc: store.JSON(`{"sdk":{"attempts":99999,"usage":{"input_tokens":99999}}}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), legacy) })
	read("/api/runs/"+legacy+"/tokens", 409, nil)
	read("/api/runs/"+prefix+"-missing/tokens", 404, nil)
	var after httpapi.StatsTokens
	read("/api/stats/tokens", 200, &after)
	const sum = records * (records + 1) / 2
	if after.TotalCalls-before.TotalCalls != 2*records || after.InputTokens-before.InputTokens != 10*sum || after.CachedInputTokens-before.CachedInputTokens != 2*sum || after.OutputTokens-before.OutputTokens != sum || after.ReasoningTokens != 0 {
		t.Fatalf("SDK totals must include all receipts once: before=%+v after=%+v", before, after)
	}
	if after.CacheHitRate != float64(after.CachedInputTokens)/float64(after.InputTokens) {
		t.Fatalf("unweighted cache hit rate: %v", after.CacheHitRate)
	}
	if len(after.ByRun) != 15 {
		t.Fatalf("chart rows=%d", len(after.ByRun))
	}
	for i, row := range after.ByRun {
		index := i + 2
		if row.RunID != ids[index] || row.DAG != "node-workflow" || row.StartedAt != start.Add(time.Duration(index)*time.Minute).UTC().Format(time.RFC3339Nano) || row.InputTokens != 10*(index+1) || row.CachedInputTokens != 2*(index+1) || row.OutputTokens != index+1 {
			t.Fatalf("chart metadata/chronology at %d: %+v", i, row)
		}
	}
}

func TestSDKTokenContinuationChainCountsOnlyNewExecutionUsage(t *testing.T) {
	_, server := newTestServer(t, nil)
	st := openStore(t)
	read := func(path string, output any) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", path, response.StatusCode)
		}
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
	var before httpapi.StatsTokens
	read("/api/stats/tokens", &before)
	prefix := fmt.Sprintf("sdk-token-continuation-%d", time.Now().UnixNano())
	start := time.Now().UTC().AddDate(20, 0, 0)
	checkpoint := func(calls, input, cached, output int) map[string]any {
		return map[string]any{"phase": "interrupted", "attempts": calls, "usage": map[string]int{"input_tokens": input, "cached_input_tokens": cached, "output_tokens": output}}
	}
	// A root, two successive continuations, then an interrupted approval renewal
	// with no new turn. Each successor carries its complete prior budget usage.
	snapshots := []map[string]any{checkpoint(2, 100, 25, 20), checkpoint(3, 120, 30, 24), checkpoint(5, 170, 40, 34), checkpoint(5, 170, 40, 34)}
	var ids []string
	for i, snapshot := range snapshots {
		id := fmt.Sprintf("%s-%d", prefix, i)
		ids = append(ids, id)
		options := map[string]any{"runtime": "codex-sdk"}
		if i > 0 {
			options["request"] = map[string]any{"continuation": map[string]any{"checkpoint": snapshots[i-1]}}
		}
		encodedOptions, _ := json.Marshal(options)
		doc, _ := json.Marshal(map[string]any{"sdk": snapshot})
		if err := st.CreateRun(t.Context(), store.Run{RunID: id, DAG: "node-workflow", State: "interrupted", StartedAt: start.Add(time.Duration(i) * time.Minute), Options: encodedOptions, Doc: doc}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), id) })
	}
	assertStats := func(calls, input, cached, output int) {
		t.Helper()
		var got httpapi.StatsTokens
		read("/api/stats/tokens", &got)
		if got.TotalCalls-before.TotalCalls != calls || got.InputTokens-before.InputTokens != input || got.CachedInputTokens-before.CachedInputTokens != cached || got.OutputTokens-before.OutputTokens != output {
			t.Fatalf("continued usage counted again: before=%+v after=%+v", before, got)
		}
		for i, want := range [][3]int{{100, 25, 20}, {20, 5, 4}, {50, 10, 10}, {0, 0, 0}} {
			for _, row := range got.ByRun {
				if row.RunID == ids[i] && (row.InputTokens != want[0] || row.CachedInputTokens != want[1] || row.OutputTokens != want[2]) {
					t.Fatalf("chart must show execution delta for %s: %+v", row.RunID, row)
				}
			}
		}
	}
	assertStats(5, 170, 40, 34)
	var cumulative httpapi.RunTokens
	read("/api/runs/"+ids[2]+"/tokens", &cumulative)
	if cumulative.Calls != 5 || cumulative.InputTokens != 170 || cumulative.CachedInputTokens != 40 || cumulative.OutputTokens != 34 {
		t.Fatalf("run budget display must remain cumulative: %+v", cumulative)
	}
	// Removing an ancestor removes only its own contribution; successors never
	// consult that record to determine their independent execution delta.
	if err := st.DeleteRun(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	assertStats(3, 70, 15, 14)
	if err := st.DeleteRun(t.Context(), ids[1]); err != nil {
		t.Fatal(err)
	}
	assertStats(2, 50, 10, 10)
}
