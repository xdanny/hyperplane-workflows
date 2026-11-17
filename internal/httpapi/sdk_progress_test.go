package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A run that is working must look like a run that is working. This drives a
// live two-step workflow through the real SDK transport and checkpoint
// protocol, polling the run the way a reader does, and requires that:
//
//   - the FIRST committed record already lists the declared steps, so the
//     window between admission and the worker's first checkpoint does not read
//     as a run with nothing in it;
//   - every step change reaches the row while the run is still going, not only
//     at the end;
//   - updated_at moves with it, so a staleness indicator has something to read.
func TestSDKRunRowReportsProgressWhileRunning(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-progress"))
	t.Setenv("SDK_FIXTURE_CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("SDK_FIXTURE_TURN_SECONDS", "0.8")
	_, ts := newTestServer(t, nil)

	nodes := []sdkworkflow.Node{
		{ID: "collect", Kind: "task", Title: "Collect the numbers", Instruction: "Write out.txt with the answer"},
		{ID: "explain", Kind: "task", Title: "Explain the numbers", Instruction: "Write out.txt explaining the answer"},
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": "Report the numbers", "nodes": nodes})
	var launch struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launch) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}

	type observation struct {
		phase     string
		steps     [2]string
		updatedAt time.Time
		state     string
	}
	var seen []observation
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
		var detail struct{ Run store.Run }
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		var doc struct {
			SDK struct {
				Phase string `json:"phase"`
				Nodes []struct {
					ID    string `json:"id"`
					Phase string `json:"phase"`
				} `json:"nodes"`
			} `json:"sdk"`
		}
		if err := json.Unmarshal(detail.Run.Doc, &doc); err != nil {
			t.Fatal(err)
		}
		current := observation{phase: doc.SDK.Phase, updatedAt: detail.Run.UpdatedAt, state: detail.Run.State}
		for _, node := range doc.SDK.Nodes {
			for i, declared := range nodes {
				if declared.ID == node.ID {
					current.steps[i] = node.Phase
				}
			}
		}
		if len(seen) == 0 || seen[len(seen)-1] != current {
			seen = append(seen, current)
		}
		if detail.Run.State != "running" {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if len(seen) == 0 {
		t.Fatal("the run was never readable")
	}
	final := seen[len(seen)-1]
	if final.state != "passed" {
		t.Fatalf("run did not pass: %+v (%d observations)", final, len(seen))
	}

	// The first record a reader can see already describes the workflow.
	if seen[0].steps[0] == "" || seen[0].steps[1] == "" {
		t.Fatalf("the first committed record listed no steps: %+v", seen[0])
	}

	var sawFirstRunning, sawSecondRunning bool
	for _, o := range seen {
		if o.state != "running" {
			continue
		}
		if o.steps[0] == "running" && o.steps[1] == "pending" {
			sawFirstRunning = true
		}
		if o.steps[0] == "passed" && (o.steps[1] == "running" || o.steps[1] == "saving") {
			sawSecondRunning = true
		}
	}
	if !sawFirstRunning || !sawSecondRunning {
		t.Fatalf("step progress never reached the run row while it was running: %+v", seen)
	}

	// Each committed checkpoint moves updated_at, so "last updated" is real.
	for i := 1; i < len(seen); i++ {
		if !seen[i].updatedAt.After(seen[i-1].updatedAt) {
			t.Fatalf("the row changed without updated_at moving: %+v then %+v", seen[i-1], seen[i])
		}
	}
}

// A run used to record only that an acceptance command passed. Later steps then
// wrote "the caller's acceptance command was not run", because from inside the
// sandbox that is what it looks like. This requires the host's own observation
// — command, exit code, duration and a bounded output tail — on the checkpoint,
// and the command named in the document later steps read.
func TestSDKAcceptanceEvidenceReachesCheckpointAndHandoff(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-progress"))
	t.Setenv("SDK_FIXTURE_CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("SDK_FIXTURE_TURN_SECONDS", "0")
	check := filepath.Join(t.TempDir(), "check.py")
	if err := os.WriteFile(check, []byte("from pathlib import Path\nassert Path('out.txt').read_text() == '42'\nprint('checked 1 file')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, ts := newTestServer(t, nil)

	nodes := []sdkworkflow.Node{
		{ID: "collect", Kind: "task", Title: "Collect the numbers", Instruction: "Write out.txt with the answer", Check: []string{"python3", check}},
		{ID: "explain", Title: "Explain the numbers", Instruction: "Read the checked file and explain it"},
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": "Report the numbers", "nodes": nodes})
	var launch struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launch) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}
	var detail struct{ Run store.Run }
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Run.State != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if detail.Run.State != "passed" {
		t.Fatalf("run did not pass: %s %s", detail.Run.State, detail.Run.Doc)
	}
	var doc struct {
		SDK struct {
			Nodes []struct {
				ID                 string `json:"id"`
				VerificationDetail *struct {
					Command    []string `json:"command"`
					ExitCode   *int     `json:"exitCode"`
					DurationMs *int64   `json:"durationMs"`
					Stdout     string   `json:"stdout"`
					Stderr     string   `json:"stderr"`
				} `json:"verificationDetail"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if err := json.Unmarshal(detail.Run.Doc, &doc); err != nil {
		t.Fatal(err)
	}
	var evidence = doc.SDK.Nodes[0].VerificationDetail
	if evidence == nil {
		t.Fatalf("the checkpoint recorded no acceptance evidence: %s", detail.Run.Doc)
	}
	if evidence.ExitCode == nil || *evidence.ExitCode != 0 || evidence.DurationMs == nil {
		t.Fatalf("acceptance evidence lost its exit code or duration: %+v", evidence)
	}
	if len(evidence.Command) != 2 || evidence.Command[0] != "python3" {
		t.Fatalf("acceptance evidence did not name the command: %+v", evidence)
	}
	if !strings.Contains(evidence.Stdout, "checked 1 file") {
		t.Fatalf("acceptance evidence dropped what the check printed: %+v", evidence)
	}
	if doc.SDK.Nodes[1].VerificationDetail != nil {
		t.Fatal("a step with no acceptance command recorded one anyway")
	}

	// The document later steps read names what the host ran and its result.
	resp, err := http.Get(ts.URL + "/file/" + launch.RunID + "/nodes/delivery/OUTPUTS/collect.md")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	handoff, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handoff), "The host ran") || !strings.Contains(string(handoff), "check.py") || !strings.Contains(string(handoff), "exited 0") {
		t.Fatalf("the handoff does not say what the host ran: %s", handoff)
	}
}
