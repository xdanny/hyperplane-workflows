package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKNodeWorkflowSavedLaunchAndCancellation(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/nodes"))
	for _, mode := range []string{"ordinary", "transport", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			srv, ts := newTestServer(t, nil)
			nodes := []sdkworkflow.Node{{ID: "explore", Title: "Explore", Instruction: "Find options"}, {ID: "review", Title: "Review", Instruction: "Compare options"}}
			workflow := store.SDKWorkflow{Name: "Choose an option", Kind: "nodes", Instruction: "Choose an option", Nodes: nodes}
			payload, _ := json.Marshal(workflow)
			resp, err := http.Post(ts.URL+"/api/workflows", "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 201 {
				data, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				t.Fatalf("save: %d %s", resp.StatusCode, data)
			}
			err = json.NewDecoder(resp.Body).Decode(&workflow)
			resp.Body.Close()
			if err != nil || workflow.ID == "" || workflow.Revision != 1 || len(workflow.Nodes) != 2 {
				t.Fatalf("saved: %+v %v", workflow, err)
			}
			resp, err = http.Get(ts.URL + "/api/workflows")
			if err != nil {
				t.Fatal(err)
			}
			var library []store.SDKWorkflow
			err = json.NewDecoder(resp.Body).Decode(&library)
			resp.Body.Close()
			var loaded store.SDKWorkflow
			for _, item := range library {
				if item.ID == workflow.ID {
					loaded = item
				}
			}
			if err != nil || len(loaded.Nodes) != 2 || loaded.Nodes[1].Instruction != "Compare options" {
				t.Fatalf("load: %+v %v", library, err)
			}
			payload, _ = json.Marshal(map[string]any{"nodes": loaded.Nodes, "goal": "Unsaved execution goal", "sourceWorkflowId": loaded.ID, "workflowName": "Unsaved display name"})
			resp, err = http.Post(ts.URL+"/api/run", "application/json", bytes.NewReader(payload))
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
				Run store.Run `json:"run"`
			}
			var snapshot struct {
				SDK struct {
					Nodes    []struct{ ID, Phase string }
					ThreadID string `json:"threadId"`
					Attempts int
				} `json:"sdk"`
			}
			cancelled := false
			deadline := time.Now().Add(12 * time.Second)
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
				if err = json.Unmarshal(detail.Run.Doc, &snapshot); err != nil {
					t.Fatal(err)
				}
				if mode == "hang" && !cancelled && len(snapshot.SDK.Nodes) == 2 && snapshot.SDK.Nodes[1].Phase == "running" {
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
			want := map[string]string{"ordinary": "passed", "transport": "failed", "hang": "interrupted"}[mode]
			if !srv.DrainRuns(5*time.Second) || detail.Run.State != want {
				t.Fatalf("state: %s want %s", detail.Run.State, want)
			}
			if len(snapshot.SDK.Nodes) != 2 || snapshot.SDK.Nodes[0].Phase != "passed" || snapshot.SDK.ThreadID != "fixture-nodes-thread" {
				t.Fatalf("progress missing: %s", detail.Run.Doc)
			}
			var options map[string]json.RawMessage
			if err := json.Unmarshal(detail.Run.Options, &options); err != nil {
				t.Fatal(err)
			}
			var identity struct{ Key, Name string }
			if err := json.Unmarshal(options["projectWorkflow"], &identity); err != nil || identity.Key != "saved:"+workflow.ID || identity.Name != "Unsaved display name" {
				t.Fatalf("HTTP launch lost source metadata: %s (%v)", detail.Run.Options, err)
			}
			var request struct {
				Instruction string
				Workflow    string             `json:"workflow"`
				Nodes       []sdkworkflow.Node `json:"nodes"`
			}
			if err := json.Unmarshal(options["request"], &request); err != nil || request.Workflow != "node-workflow" || request.Instruction != "Unsaved execution goal" || len(request.Nodes) != 2 || options["spec"] != nil {
				t.Fatalf("SDK launch must retain its actual request without a graph spec: %s (%v)", detail.Run.Options, err)
			}
			resp, err = http.Get(ts.URL + "/api/runs/" + launch.RunID + "/graph")
			if err != nil {
				t.Fatal(err)
			}
			graphBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("SDK run must not fall back to graph lookup: %d %s", resp.StatusCode, graphBody)
			}
			for _, node := range nodes {
				resp, err = http.Get(ts.URL + "/file/" + launch.RunID + "/nodes/delivery/OUTPUTS/" + node.ID + ".md")
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if node.ID == "explore" || mode == "ordinary" {
					if resp.StatusCode != 200 || !strings.HasPrefix(string(data), "# "+node.Title) {
						t.Fatalf("document: %d %s", resp.StatusCode, data)
					}
				} else if resp.StatusCode != 404 {
					t.Fatalf("unrun document: %d", resp.StatusCode)
				}
			}
		})
	}
}

func TestSDKNodeWorkflowRejectsInvalidDefinitionBeforeLaunch(t *testing.T) {
	_, ts := newTestServer(t, nil)
	client := &http.Client{Timeout: 2 * time.Second}
	for _, payload := range []string{
		`{"goal":"Choose","nodes":[]}`,
		`{"goal":"Choose","nodes":[{"id":"a","title":"Explore","instruction":"Work","deps":["missing"]}]}`,
		`{"goal":"Choose","nodes":[{"id":"../escape","title":"Explore","instruction":"Work"}]}`,
		`{"goal":"Choose","nodes":[{"id":"a","title":"Explore","instruction":""}]}`,
		`{"goal":"Choose","nodes":[{"id":"a","title":"Explore","instruction":"Work"}],"check":["true"]}`,
	} {
		resp, err := client.Post(ts.URL+"/api/run", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("accepted invalid workflow: %d", resp.StatusCode)
		}
	}
}
