package httpapi_test

import (
	"bytes"
	"encoding/json"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Uses real HTTP, isolated PostgreSQL, SDK conversations and host acceptance.
func TestSDKBacklogLifecycle(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/task"))
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"recovery", "hang", "transport"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			srv, ts := newTestServer(t, nil)
			body, _ := json.Marshal(map[string]any{"project": "sdk-backlog", "targetRoot": t.TempDir(), "autoRepair": true, "stopOnFail": true, "backlog": []map[string]any{
				{"title": "Write an answer", "check": []string{python, "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42', 'answer must equal 42'"}},
				{"title": "Write another answer", "check": []string{python, "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42', 'answer must equal 42'"}},
			}})
			res, err := http.Post(ts.URL+"/api/orchestrate", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var launch struct {
				ID string `json:"id"`
			}
			json.NewDecoder(res.Body).Decode(&launch)
			res.Body.Close()
			if res.StatusCode != 202 || launch.ID == "" {
				t.Fatalf("launch %d %+v", res.StatusCode, launch)
			}
			var batch store.OrchestrateRunRecord
			cancelled := false
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				res, err = http.Get(ts.URL + "/api/orchestrate/" + launch.ID)
				if err != nil {
					t.Fatal(err)
				}
				err = json.NewDecoder(res.Body).Decode(&batch)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "hang" && !cancelled {
					if data, _ := os.ReadFile(calls); len(data) > 0 {
						res, err = http.Post(ts.URL+"/api/orchestrate/"+launch.ID+"/cancel", "application/json", nil)
						if err != nil {
							t.Fatal(err)
						}
						res.Body.Close()
						cancelled = true
					}
				}
				if batch.Status != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !srv.DrainRuns(5 * time.Second) {
				t.Fatal("SDK batch did not drain")
			}
			var items []struct {
				RunID string `json:"runId"`
				State string `json:"state"`
			}
			json.Unmarshal(batch.Items, &items)
			if mode == "recovery" {
				data, _ := os.ReadFile(calls)
				if batch.Status != "done" || batch.Done != 2 || len(items) != 2 || string(data) != "start\nresume\nstart\nresume\n" {
					t.Fatalf("batch=%+v calls=%s", batch, data)
				}
			} else {
				expected := "failed"
				if mode == "hang" {
					expected = "interrupted"
				}
				if batch.Status != expected || batch.Failed != 1 || len(items) != 1 {
					t.Fatalf("batch=%+v", batch)
				}
			}
			for _, item := range items {
				res, err = http.Get(ts.URL + "/api/runs/" + item.RunID)
				if err != nil {
					t.Fatal(err)
				}
				var detail struct {
					Run   store.Run    `json:"run"`
					Nodes []store.Node `json:"nodes"`
				}
				json.NewDecoder(res.Body).Decode(&detail)
				res.Body.Close()
				if detail.Run.DAG != "checked-task" || detail.Run.Project != "sdk-backlog" || len(detail.Nodes) != 1 || detail.Nodes[0].Kind != "sdk" {
					t.Fatalf("unexpected SDK task: %+v", detail)
				}
				if !bytes.Contains(detail.Run.Options, []byte("answer must equal 42")) {
					t.Fatal("lost fixed acceptance command")
				}
			}
		})
	}
}

func TestSDKBacklogRejectsMissingChecksBeforeStarting(t *testing.T) {
	_, ts := newTestServer(t, nil)
	res, err := http.Post(ts.URL+"/api/orchestrate", "application/json", bytes.NewBufferString(`{"backlog":[{"title":"do work"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status=%d", res.StatusCode)
	}
}
