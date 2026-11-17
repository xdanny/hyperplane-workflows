package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKCheckedWorkNodes(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-task"))
	for _, mode := range []string{"ordinary", "general", "general-empty", "correction", "reject", "human-reject", "changed", "added", "symlink", "hang", "large", "large-changed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			external := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(external, []byte("42"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SDK_FIXTURE_EXTERNAL", external)
			acceptance := filepath.Join(t.TempDir(), "acceptance check.py")
			checkCalls := filepath.Join(t.TempDir(), "check-calls")
			t.Setenv("SDK_FIXTURE_CHECK_CALLS", checkCalls)
			if err := os.WriteFile(acceptance, []byte("from pathlib import Path\nimport os\nwith open(os.environ['SDK_FIXTURE_CHECK_CALLS'],'a') as f: f.write('checked\\n')\nassert Path('answer.txt').read_text() == '42'\nprint('actual acceptance executed')\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "large") {
				file, err := os.OpenFile(acceptance, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString("with open('data.csv') as rows: assert sum(1 for _ in rows) == 1000001\n")
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			srv, ts := newTestServer(t, nil)
			nodes := []sdkworkflow.Node{
				{ID: "build", Kind: "task", Title: "Build answer", Instruction: "Create answer.txt with the answer", Check: []string{"python3", acceptance}},
				{ID: "approval", Kind: "approval", Title: "Approve the file", Instruction: "Inspect the actual file before continuing"},
				{ID: "summary", Title: "Summarize", Instruction: "Read the checked file and summarize it"},
			}
			if strings.HasPrefix(mode, "general") {
				nodes[0].Check = nil
			}
			wantCommand := len(nodes[0].Check)
			status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/workflows", store.SDKWorkflow{Name: "Checked answer", Kind: "nodes", Instruction: "Produce a checked answer", Nodes: nodes, AutoRepair: mode == "correction"})
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil || len(saved.Nodes[0].Check) != wantCommand {
				t.Fatalf("save: %d %s", status, raw)
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": saved.Instruction, "nodes": saved.Nodes, "autoRepair": saved.AutoRepair})
			var launch struct{ RunID string }
			if status != 202 || json.Unmarshal(raw, &launch) != nil {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var detail struct {
				Run   store.Run
				Nodes []store.Node
			}
			acted := false
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				var snapshot struct {
					SDK struct {
						Phase, ThreadID string
						Approval        *struct {
							ID        string
							Documents []struct{ Name, URL, SHA256 string }
						}
					}
				}
				if err := json.Unmarshal(detail.Run.Doc, &snapshot); err != nil {
					t.Fatal(err)
				}
				if !acted && mode == "hang" && snapshot.SDK.ThreadID == "fixture-task" {
					status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/runs/"+launch.RunID+"/cancel", nil)
					if status != 200 {
						t.Fatalf("cancel: %d %s", status, raw)
					}
					acted = true
				}
				if !acted && snapshot.SDK.Phase == "approval_pending" && snapshot.SDK.Approval != nil {
					approval := snapshot.SDK.Approval
					wantDocuments := 2
					if strings.HasPrefix(mode, "large") {
						wantDocuments = 3
					}
					if len(approval.Documents) != wantDocuments {
						t.Fatalf("approval omitted actual work file: %+v", approval)
					}
					var fileURL, largeURL string
					for _, doc := range approval.Documents {
						if doc.Name == "build/data.csv" {
							largeURL = doc.URL
						}
						if doc.Name == "build/answer.txt" {
							fileURL = doc.URL
							if len(doc.SHA256) != 64 {
								t.Fatal("file hash missing")
							}
						}
					}
					if fileURL == "" {
						t.Fatal("no work file link")
					}
					status, data := doJSON(t, http.MethodGet, ts.URL+fileURL, nil)
					if status != 200 || string(data) != "42" {
						t.Fatalf("work file: %d %s", status, data)
					}
					if strings.HasPrefix(mode, "large") {
						if largeURL == "" {
							t.Fatal("large file missing from approval")
						}
						response, err := http.Get(ts.URL + largeURL)
						if err != nil {
							t.Fatal(err)
						}
						count, err := io.CopyBuffer(io.Discard, response.Body, make([]byte, 256*1024))
						response.Body.Close()
						if err != nil || response.StatusCode != 200 || count != 64_000_010 {
							t.Fatalf("large download: status=%d bytes=%d error=%v", response.StatusCode, count, err)
						}
					}
					callData, _ := os.ReadFile(calls)
					if strings.Contains(string(callData), "summary") {
						t.Fatal("advanced before approval")
					}
					if mode == "changed" || mode == "added" || mode == "large-changed" {
						var options struct{ Request struct{ Workspace string } }
						if err := json.Unmarshal(detail.Run.Options, &options); err != nil {
							t.Fatal(err)
						}
						name := "answer.txt"
						if mode == "added" {
							name = "unexpected.txt"
						}
						if mode == "large-changed" {
							file, err := os.OpenFile(filepath.Join(options.Request.Workspace, "build", "data.csv"), os.O_WRONLY, 0)
							if err != nil {
								t.Fatal(err)
							}
							_, err = file.WriteAt([]byte("9"), 60_000_000)
							file.Close()
							if err != nil {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(filepath.Join(options.Request.Workspace, "build", name), []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					decision := "approved"
					if mode == "human-reject" {
						decision = "rejected"
					}
					status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+url.PathEscape(approval.ID)+"/decision", map[string]string{"decision": decision})
					if status != 200 {
						t.Fatalf("decide: %d %s", status, raw)
					}
					acted = true
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "failed"
			if mode == "ordinary" || mode == "general" || mode == "correction" || mode == "large" {
				want = "passed"
			}
			if mode == "hang" {
				want = "interrupted"
			}
			if !srv.DrainRuns(5*time.Second) || detail.Run.State != want || len(detail.Nodes) != 1 || detail.Nodes[0].Kind != "sdk" {
				t.Fatalf("lifecycle: %s want %s: %s", detail.Run.State, want, detail.Run.Doc)
			}
			callData, _ := os.ReadFile(calls)
			wantCalls := "build\n"
			if mode == "correction" {
				wantCalls += "build\n"
			}
			if want == "passed" {
				wantCalls += "summary\n"
			}
			if string(callData) != wantCalls {
				t.Fatalf("calls %q want %q", callData, wantCalls)
			}
			if want == "passed" && (!acted || !strings.Contains(string(detail.Run.Doc), "build/answer.txt")) {
				t.Fatal("lost file receipt or approval")
			}
			checks, _ := os.ReadFile(checkCalls)
			wantChecks := 1
			if mode == "correction" {
				wantChecks = 2
			}
			if mode == "symlink" || mode == "hang" || strings.HasPrefix(mode, "general") {
				wantChecks = 0
			}
			if strings.Count(string(checks), "checked\n") != wantChecks {
				t.Fatalf("acceptance calls: %q, want %d", checks, wantChecks)
			}
			if mode == "symlink" {
				status, _ = doJSON(t, http.MethodGet, ts.URL+"/file/"+launch.RunID+"/nodes/delivery/OUTPUTS/build/answer.txt", nil)
				if status == 200 {
					t.Fatal("served escaping work symlink")
				}
			}
		})
	}
}
