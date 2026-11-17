package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKContinuationHTTPReviewLaunchAndIdempotentRetry(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/nodes"))
	t.Setenv("SDK_FIXTURE_MODE", "hang")
	t.Setenv("SDK_FIXTURE_CONTINUATION", "0")
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	nodes := []sdkworkflow.Node{{ID: "explore", Title: "Explore", Instruction: "Find options"}, {ID: "review", Title: "Review", Instruction: "Compare options"}}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"nodes": nodes, "goal": "Choose an option", "project": "continuation-http"})
	var launch struct {
		RunID string `json:"runId"`
	}
	if status != 202 || json.Unmarshal(raw, &launch) != nil || launch.RunID == "" {
		t.Fatalf("launch: %d %s", status, raw)
	}
	sourceID := launch.RunID
	url := ts.URL + "/api/runs/" + sourceID + "/continuation"
	status, raw = doJSON(t, http.MethodGet, url, nil)
	var review sdkruntime.ContinuationReview
	if status != 200 || json.Unmarshal(raw, &review) != nil || review.Eligible {
		t.Fatalf("running review: %d %s", status, raw)
	}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		run, err := st.GetRun(t.Context(), sourceID)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			SDK struct{ Nodes []struct{ ID, Phase string } }
		}
		if err = json.Unmarshal(run.Doc, &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.SDK.Nodes) == 2 && snapshot.SDK.Nodes[0].Phase == "passed" && snapshot.SDK.Nodes[1].Phase == "running" {
			recorded, _ := os.ReadFile(calls)
			if strings.Contains(string(recorded), "review\n") {
				ready = true
				break
			}
		}
		if run.State != "running" {
			t.Fatalf("source stopped before intended interruption: %s", run.Doc)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("source did not reach its second step")
	}
	if status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/runs/"+sourceID+"/cancel", nil); status != 200 {
		t.Fatalf("cancel: %d %s", status, raw)
	}
	// Wait for the actual worker to release ownership, not only the cancellation
	// row. Continue must never race a still-stopping source worker.
	released := false
	for deadline = time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		owner, err := st.OwnSDKRun(t.Context(), sourceID)
		if err == nil {
			owner.Close()
			released = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !released {
		t.Fatal("source worker did not stop")
	}
	before, err := st.GetRun(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	status, raw = doJSON(t, http.MethodGet, url, nil)
	if status != 200 || json.Unmarshal(raw, &review) != nil || !review.Eligible || len(review.ReviewToken) != 64 || len(review.Reused) != 1 || review.Reused[0].ID != "explore" || review.Restart == nil || review.Restart.ID != "review" {
		t.Fatalf("review: %d %s", status, raw)
	}
	t.Run("unavailable validator", func(t *testing.T) {
		t.Setenv("HYPERPLANE_PLATFORM_ROOT", t.TempDir())
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var body any
			if method == http.MethodPost {
				body = map[string]string{"reviewToken": review.ReviewToken}
			}
			if status, raw := doJSON(t, method, url, body); status != 503 {
				t.Fatalf("unavailable: %d %s", status, raw)
			}
		}
	})
	if status, raw = doJSON(t, http.MethodPost, url, map[string]string{"reviewToken": strings.Repeat("0", 64)}); status != 409 {
		t.Fatalf("stale token: %d %s", status, raw)
	}
	blocker, err := st.OwnSDKRun(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	status, raw = doJSON(t, http.MethodPost, url, map[string]string{"reviewToken": review.ReviewToken})
	blocker.Close()
	if status != 409 {
		t.Fatalf("owned source: %d %s", status, raw)
	}
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	t.Setenv("SDK_FIXTURE_CONTINUATION", "1")
	status, raw = doJSON(t, http.MethodPost, url, map[string]string{"reviewToken": review.ReviewToken})
	var child struct {
		RunID string `json:"runId"`
	}
	if status != 202 || json.Unmarshal(raw, &child) != nil || child.RunID == "" || child.RunID == sourceID {
		t.Fatalf("continue: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodPost, url, map[string]string{"reviewToken": review.ReviewToken})
	var retry struct {
		RunID string `json:"runId"`
	}
	if status != 202 || json.Unmarshal(raw, &retry) != nil || retry.RunID != child.RunID {
		t.Fatalf("idempotent retry: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodGet, url, nil)
	var linked sdkruntime.ContinuationReview
	if status != 200 || json.Unmarshal(raw, &linked) != nil || linked.Eligible || linked.ContinuedRunID != child.RunID {
		t.Fatalf("source link: %d %s", status, raw)
	}
	var final store.Run
	for deadline = time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		final, err = st.GetRun(t.Context(), child.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != "passed" || final.ParentRun != sourceID || final.Project != before.Project {
		t.Fatalf("successor: %+v", final)
	}
	after, err := st.GetRun(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeDoc, afterDoc map[string]json.RawMessage
	_ = json.Unmarshal(before.Doc, &beforeDoc)
	_ = json.Unmarshal(after.Doc, &afterDoc)
	if after.State != "interrupted" || !bytes.Equal(beforeDoc["sdk"], afterDoc["sdk"]) || !bytes.Equal(before.Summary, after.Summary) {
		t.Fatal("source receipt changed")
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(recorded), "explore\n") != 1 || strings.Count(string(recorded), "review\n") != 2 {
		t.Fatalf("completed work was replayed: %s", recorded)
	}
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/journeys?project=continuation-http", nil)
	var deliveries []journey.Journey
	if status != 200 || json.Unmarshal(raw, &deliveries) != nil || len(deliveries) != 1 || deliveries[0].AttemptCount != 2 || deliveries[0].Attempts[0].RunID != child.RunID || deliveries[0].Attempts[0].ContinuedFrom != sourceID || deliveries[0].Criteria != 0 || deliveries[0].Maturity != journey.MaturityUnproven || !deliveries[0].NeedsDecision {
		t.Fatalf("continued delivery hidden or ordinary completion counted as proof: %d %s", status, raw)
	}
	deliveryKey := deliveries[0].Key
	if status, raw = doJSON(t, http.MethodDelete, ts.URL+"/api/runs/"+sourceID, nil); status != 200 {
		t.Fatalf("delete stopped source: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/journeys?project=continuation-http", nil)
	if status != 200 || json.Unmarshal(raw, &deliveries) != nil || len(deliveries) != 1 || deliveries[0].Key != deliveryKey || deliveries[0].AttemptCount != 1 || deliveries[0].Attempts[0].RunID != child.RunID {
		t.Fatalf("source deletion changed continued delivery identity: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/journeys/"+deliveryKey+"/outcome", map[string]string{"runId": child.RunID, "verdict": "accepted", "note": "The continued documents meet my request."})
	if status != http.StatusCreated {
		t.Fatalf("continued human outcome: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+child.RunID, nil)
	var detail struct {
		Journey struct {
			Key     string           `json:"key"`
			Outcome *journey.Outcome `json:"outcome"`
		} `json:"journey"`
	}
	if status != 200 || json.Unmarshal(raw, &detail) != nil || detail.Journey.Key != deliveryKey || detail.Journey.Outcome == nil || detail.Journey.Outcome.Verdict != "accepted" {
		t.Fatalf("continued run detail lost its own decision: %d %s", status, raw)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var body any
		if method == http.MethodPost {
			body = map[string]string{"reviewToken": review.ReviewToken}
		}
		if status, raw := doJSON(t, method, ts.URL+"/api/runs/missing-continuation-source/continuation", body); status != 404 {
			t.Fatalf("missing source: %d %s", status, raw)
		}
	}
}
