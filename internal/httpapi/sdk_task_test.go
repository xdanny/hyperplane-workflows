package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKDirectTaskLifecycle(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "task"))
	for _, mode := range []string{"ordinary", "recovery", "hang", "no-check", "check-fails"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "no-check" || mode == "check-fails" {
				fixtureMode = "recovery"
			}
			t.Setenv("SDK_FIXTURE_MODE", fixtureMode)
			srv, ts := newTestServer(t, nil)
			body := map[string]any{"task": "Write an answer", "targetRoot": t.TempDir(), "autoRepair": mode != "check-fails"}
			if mode != "no-check" {
				body["check"] = []string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42'"}
			}
			payload, _ := json.Marshal(body)
			resp, err := http.Post(ts.URL+"/api/run", "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			var launch struct {
				RunID string `json:"runId"`
			}
			err = json.NewDecoder(resp.Body).Decode(&launch)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 202 || launch.RunID == "" {
				t.Fatalf("launch: %d %+v %v", resp.StatusCode, launch, err)
			}
			var detail struct {
				Run   store.Run    `json:"run"`
				Nodes []store.Node `json:"nodes"`
			}
			deadline := time.Now().Add(12 * time.Second)
			cancelled := false
			for time.Now().Before(deadline) {
				resp, err = http.Get(ts.URL + "/api/runs/" + launch.RunID)
				if err != nil {
					t.Fatal(err)
				}
				err = json.NewDecoder(resp.Body).Decode(&detail)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "hang" && !cancelled && bytes.Contains(detail.Run.Doc, []byte("fixture-task-thread")) {
					resp, err = http.Post(ts.URL+"/api/runs/"+launch.RunID+"/cancel", "application/json", nil)
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					if resp.StatusCode != 200 {
						t.Fatalf("cancel: %d", resp.StatusCode)
					}
					cancelled = true
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "passed"
			if mode == "check-fails" {
				want = "failed"
			}
			if mode == "hang" {
				want = "interrupted"
			}
			if !srv.DrainRuns(5*time.Second) || detail.Run.State != want || len(detail.Nodes) != 1 || detail.Nodes[0].Kind != "sdk" {
				t.Fatalf("task lifecycle: %+v", detail)
			}
			if mode != "hang" {
				var doc struct {
					SDK struct {
						Attempts     int    `json:"attempts"`
						Verification string `json:"verification"`
					} `json:"sdk"`
				}
				attempts, verification := 1, "passed"
				if mode == "recovery" {
					attempts = 2
				}
				if mode == "no-check" {
					verification = "not_requested"
				}
				if mode == "check-fails" {
					verification = "failed"
				}
				if err := json.Unmarshal(detail.Run.Doc, &doc); err != nil || doc.SDK.Attempts != attempts || doc.SDK.Verification != verification {
					t.Fatalf("incorrect verification or correction: %s (%v)", detail.Run.Doc, err)
				}
			}
		})
	}
}

func TestSDKDirectTaskRejectsInvalidLaunchWithoutHanging(t *testing.T) {
	_, ts := newTestServer(t, nil)
	client := &http.Client{Timeout: 2 * time.Second}
	target, _ := json.Marshal(t.TempDir())
	longName, _ := json.Marshal(strings.Repeat("é", 81))
	for _, payload := range []string{
		`{"task":"work","targetRoot":"/does-not-exist","check":["true"]}`,
		`{"task":"work","targetRoot":` + string(target) + `,"check":[]}`,
		`{"task":"work","targetRoot":` + string(target) + `,"check":[" "]}`,
		`{"task":"work","targetRoot":` + string(target) + `,"check":[12]}`,
		`{"task":"work","check":["true"],"dag":"hello"}`,
		`{"task":"work","check":["true"],"dryRun":true}`,
		`{"task":"work","targetRoot":` + string(target) + `,"sourceWorkflowId":"missing"}`,
		`{"workflow":"app-ideator","sourceWorkflowId":"missing"}`,
		`{"goal":"Write","nodes":[{"id":"write","title":"Write","instruction":"Write"}],"sourceWorkflowId":"missing"}`,
		`{"workflow":"app-ideator","workflowName":` + string(longName) + `}`,
		`{"workflow":"app-ideator","projectWorkflow":{"version":1,"key":"saved:forged","name":"Forged"}}`,
		`{"workflow":"app-ideator","definitionKey":"forged"}`,
		`{"workflow":"app-ideator","sourceWorkflowId":123}`,
	} {
		resp, err := client.Post(ts.URL+"/api/run", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("invalid launch: %d", resp.StatusCode)
		}
	}
}
