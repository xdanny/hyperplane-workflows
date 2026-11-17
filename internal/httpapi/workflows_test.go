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

func TestSavedSDKWorkflowLifecycle(t *testing.T) {
	for _, checked := range []bool{true, false} {
		name := "no-check"
		if checked {
			name = "checked"
		}
		t.Run(name, func(t *testing.T) { testSavedSDKWorkflowLifecycle(t, checked) })
	}
}

func testSavedSDKWorkflowLifecycle(t *testing.T, checked bool) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "task"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	srv, ts := newTestServer(t, nil)
	call := func(method, path string, body any, status int, out any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			var detail any
			_ = json.NewDecoder(res.Body).Decode(&detail)
			t.Fatalf("%s %s: status %d: %v", method, path, res.StatusCode, detail)
		}
		if out != nil {
			if err := json.NewDecoder(res.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := store.SDKWorkflow{Name: "Answer check", Kind: "task", Instruction: "Write the answer", TargetRoot: t.TempDir(),
		Check: []string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42'"}}
	if !checked {
		w.Check = nil
	}
	var saved store.SDKWorkflow
	call("POST", "/api/workflows", w, 201, &saved)
	if saved.ID == "" || saved.Revision != 1 {
		t.Fatalf("saved: %+v", saved)
	}
	var listed []store.SDKWorkflow
	call("GET", "/api/workflows", nil, 200, &listed)
	found := false
	for _, item := range listed {
		if item.ID == saved.ID {
			found = item.Name == w.Name && item.TargetRoot == w.TargetRoot
			if checked {
				found = found && len(item.Check) == 3 && item.Check[2] == w.Check[2]
			} else {
				found = found && item.Check == nil
			}
		}
	}
	if !found {
		t.Fatal("saved setup missing from list")
	}
	stale := saved
	saved.Name = "Updated answer check"
	call("PUT", "/api/workflows/"+saved.ID, saved, 200, &saved)
	if saved.Revision != 2 {
		t.Fatalf("revision: %d", saved.Revision)
	}
	call("PUT", "/api/workflows/"+saved.ID, stale, 409, nil)
	call("DELETE", "/api/workflows/"+saved.ID+"?revision=1", nil, 409, nil)
	// Launch the exact loaded setup through the ordinary SDK route. The
	// workflow row does not become a second executable or a graph definition.
	var launch struct {
		RunID string `json:"runId"`
	}
	call("POST", "/api/run", map[string]any{"task": saved.Instruction, "targetRoot": saved.TargetRoot, "check": saved.Check}, 202, &launch)
	var detail struct {
		Run store.Run `json:"run"`
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		call("GET", "/api/runs/"+launch.RunID, nil, 200, &detail)
		if detail.Run.State != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !srv.DrainRuns(5 * time.Second) {
		t.Fatal("saved task did not finish")
	}
	if detail.Run.State != "passed" {
		t.Fatalf("run: %s %s", detail.Run.State, detail.Run.Doc)
	}
	var doc struct {
		SDK struct {
			Verification string `json:"verification"`
		} `json:"sdk"`
	}
	wantVerification := "not_requested"
	if checked {
		wantVerification = "passed"
	}
	if err := json.Unmarshal(detail.Run.Doc, &doc); err != nil || doc.SDK.Verification != wantVerification {
		t.Fatalf("incorrect verification: %s (%v)", detail.Run.Doc, err)
	}
	call("DELETE", "/api/workflows/"+saved.ID+"?revision=2", nil, 200, nil)
	call("GET", "/api/runs/"+launch.RunID, nil, 200, &detail)
	call("GET", "/api/workflows", nil, 200, &listed)
	for _, item := range listed {
		if item.ID == saved.ID {
			t.Fatal("removed setup still listed")
		}
	}
	// Invalid setup never enters the library.
	for _, check := range [][]string{{}, {" "}, {"true", "bad\x00argument"}} {
		w.Check = check
		call("POST", "/api/workflows", w, 400, nil)
	}
	w.Check = nil
	w.Kind, w.TargetRoot = "app-ideator", ""
	call("POST", "/api/workflows", w, 201, &saved)
}
