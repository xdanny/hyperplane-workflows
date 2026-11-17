package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Real HTTP, PostgreSQL, SDK, verifier and subprocess cancellation. Only model
// output comes from a fixture. No DAG or repair child is involved.
func TestSDKIdeatorLifecycle(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "ideator"))
	for _, tc := range []struct {
		mode     string
		repair   bool
		state    string
		attempts int
	}{
		{"ordinary", false, "passed", 1}, {"recovery", true, "passed", 2},
		{"recovery", false, "failed", 1}, {"transport", true, "failed", 0}, {"hang", true, "interrupted", 0},
		{"correction-transport", true, "failed", 2},
	} {
		t.Run(tc.mode+tc.state, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", tc.mode)
			srv, ts := newTestServer(t, nil)
			body, _ := json.Marshal(map[string]any{"workflow": "app-ideator", "project": "sdk-e2e", "autoRepair": tc.repair, "inputs": map[string]string{"domain": "cycling tools"}})
			res, err := http.Post(ts.URL+"/api/run", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var launch struct {
				RunID string `json:"runId"`
			}
			err = json.NewDecoder(res.Body).Decode(&launch)
			res.Body.Close()
			if err != nil || res.StatusCode != 202 || launch.RunID == "" {
				t.Fatalf("launch: %+v %d %v", launch, res.StatusCode, err)
			}
			t.Cleanup(func() {
				res, _ := http.Post(ts.URL+"/api/runs/"+launch.RunID+"/cancel", "application/json", nil)
				if res != nil {
					res.Body.Close()
				}
			})
			var detail struct {
				Run   store.Run    `json:"run"`
				Nodes []store.Node `json:"nodes"`
			}
			get := func() {
				res, err := http.Get(ts.URL + "/api/runs/" + launch.RunID)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				if err := json.NewDecoder(res.Body).Decode(&detail); err != nil {
					t.Fatal(err)
				}
			}
			cancelled := false
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				get()
				if tc.mode == "hang" && !cancelled && bytes.Contains(detail.Run.Doc, []byte("fixture-ideator-thread")) {
					res, err := http.Post(ts.URL+"/api/runs/"+launch.RunID+"/cancel", "application/json", nil)
					if err != nil {
						t.Fatal(err)
					}
					res.Body.Close()
					if res.StatusCode != 200 {
						t.Fatalf("cancel=%d", res.StatusCode)
					}
					cancelled = true
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !srv.DrainRuns(5 * time.Second) {
				t.Fatal("SDK worker did not terminate")
			}
			get()
			if detail.Run.State != tc.state || detail.Run.Project != "sdk-e2e" || len(detail.Nodes) != 1 || detail.Nodes[0].NodeID != "delivery" || detail.Nodes[0].State != tc.state {
				t.Fatalf("unexpected durable delivery: %+v", detail)
			}
			if !bytes.Contains(detail.Run.Doc, []byte("fixture-ideator-thread")) {
				t.Fatal("lost durable SDK session identity")
			}
			var options struct {
				Request struct {
					Workflow string `json:"workflow"`
				} `json:"request"`
			}
			if err := json.Unmarshal(detail.Run.Options, &options); err != nil || options.Request.Workflow != "app-ideator" {
				t.Fatalf("missing explicit SDK request: %s (%v)", detail.Run.Options, err)
			}
			if store.RunDocString(detail.Run, store.RepairedByKey) != "" {
				t.Fatal("SDK correction launched a repair graph")
			}
			if tc.attempts > 0 && detail.Nodes[0].Attempt != tc.attempts {
				t.Fatalf("attempts=%d", detail.Nodes[0].Attempt)
			}
			if tc.mode == "correction-transport" && (!bytes.Contains(detail.Run.Summary, []byte("exactly three ideas")) || !bytes.Contains(detail.Run.Summary, []byte("input_tokens"))) {
				t.Fatalf("lost first turn evidence: %s", detail.Run.Summary)
			}
			res, err = http.Get(ts.URL + "/api/runs/" + launch.RunID + "/tokens")
			if err != nil {
				t.Fatal(err)
			}
			var tokens struct {
				Input int `json:"inputTokens"`
				Calls int `json:"calls"`
			}
			json.NewDecoder(res.Body).Decode(&tokens)
			res.Body.Close()
			wantInput := 17 * tc.attempts
			if tc.mode == "correction-transport" {
				wantInput = 17
			}
			if tokens.Input != wantInput {
				t.Fatalf("SDK usage missing: %+v want input=%d", tokens, wantInput)
			}
			res, err = http.Get(ts.URL + "/api/runs/" + launch.RunID + "/log?nodeId=delivery")
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("SDK log unavailable: %d", res.StatusCode)
			}
			if tc.state == "failed" {
				res, err := http.Post(ts.URL+"/api/runs/"+launch.RunID+"/repair", "application/json", nil)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 404 {
					t.Fatalf("SDK run accepted graph repair: %d", res.StatusCode)
				}
				get()
				if store.RunDocString(detail.Run, store.RepairedByKey) != "" {
					t.Fatal("unsupported repair spent the claim")
				}
			}
			res, err = http.Get(ts.URL + "/api/runs/" + launch.RunID + "/events")
			if err != nil {
				t.Fatal(err)
			}
			var events []store.Event
			json.NewDecoder(res.Body).Decode(&events)
			res.Body.Close()
			found := false
			for _, event := range events {
				if bytes.Contains(event.Data, []byte("correction_pending")) && bytes.Contains(event.Data, []byte("exactly three ideas")) {
					found = true
				}
			}
			if tc.attempts == 2 && !found {
				t.Fatal("missing committed correction findings")
			}
			if tc.state == "passed" {
				res, err := http.Get(ts.URL + "/api/runs/" + launch.RunID + "/artifacts")
				if err != nil {
					t.Fatal(err)
				}
				var artifacts []struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				}
				json.NewDecoder(res.Body).Decode(&artifacts)
				res.Body.Close()
				if len(artifacts) != 2 {
					t.Fatalf("delivery artifacts are not visible: %+v", artifacts)
				}
				for _, a := range artifacts {
					res, err := http.Get(ts.URL + a.URL)
					if err != nil {
						t.Fatal(err)
					}
					res.Body.Close()
					if res.StatusCode != 200 {
						t.Fatalf("artifact inaccessible: %s", a.URL)
					}
				}
			}
		})
	}
}

func TestSDKIdeatorRejectsGraphLaunchAndMixedSettings(t *testing.T) {
	_, ts := newTestServer(t, nil)
	for _, body := range []string{
		`{"dag":"app-ideator"}`,
		`{"workflow":"unknown-sdk-workflow"}`,
		`{"workflow":"app-ideator","dag":"hello"}`,
		`{"workflow":"app-ideator","dryRun":true}`,
		`{"workflow":"app-ideator","autoDecompose":true}`,
		`{"workflow":"app-ideator","targetRoot":"/tmp"}`,
		`{"workflow":"app-ideator","task":"do work"}`,
		`{"workflow":"app-ideator","check":[]}`,
		`{"workflow":"app-ideator","nodes":[]}`,
		`{"workflow":"app-ideator","inputs":{"permission":"network"}}`,
	} {
		resp, err := http.Post(ts.URL+"/api/run", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d", body, resp.StatusCode)
		}
	}
	resp, err := http.Get(ts.URL + "/api/dags")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("retired graph catalog: %d", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/api/dags/app-ideator")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("synthetic ideator graph available: %d", resp.StatusCode)
	}
}
