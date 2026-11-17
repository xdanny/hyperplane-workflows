package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKApprovalFencesExecutionAndPinsDocuments(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/nodes"))
	t.Setenv("SDK_FIXTURE_APPROVAL", "1")
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	for _, mode := range []string{"approved", "rejected", "cancelled", "changed"} {
		t.Run(mode, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			srv, ts := newTestServer(t, nil)
			status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": "Choose an option with a human decision", "nodes": []map[string]string{
				{"id": "explore", "title": "Explore", "instruction": "Find options"},
				{"id": "approval", "title": "Approve options", "instruction": "Review the options before comparison", "kind": "approval"},
				{"id": "review", "title": "Review", "instruction": "Compare approved options"},
			}})
			if status != 202 {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var launch struct {
				RunID string `json:"runId"`
			}
			if err := json.Unmarshal(raw, &launch); err != nil {
				t.Fatal(err)
			}
			var detail struct {
				Run store.Run `json:"run"`
			}
			var snapshot struct {
				SDK struct {
					Phase    string
					Approval struct {
						ID        string
						Documents []struct{ Name, URL, SHA256 string }
					}
				} `json:"sdk"`
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(detail.Run.Doc, &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.SDK.Phase == "approval_pending" || detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if snapshot.SDK.Phase != "approval_pending" || snapshot.SDK.Approval.ID == "" || len(snapshot.SDK.Approval.Documents) != 1 || len(snapshot.SDK.Approval.Documents[0].SHA256) != 64 {
				t.Fatalf("no pinned review: %s", raw)
			}
			data, _ := os.ReadFile(calls)
			if string(data) != "explore\n" {
				t.Fatalf("advanced before approval: %s", data)
			}
			_, inbox := doJSON(t, http.MethodGet, ts.URL+"/api/inbox", nil)
			if !strings.Contains(string(inbox), snapshot.SDK.Approval.ID) {
				t.Fatalf("approval missing from inbox: %s", inbox)
			}
			if mode == "changed" {
				var options struct{ Request struct{ Workspace string } }
				if err := json.Unmarshal(detail.Run.Options, &options); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(options.Request.Workspace, "explore.md"), []byte("Changed after review began"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cancelled" {
				status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/runs/"+launch.RunID+"/cancel", nil)
				if status != 200 {
					t.Fatalf("cancel: %d %s", status, raw)
				}
			}
			decision := "approved"
			if mode == "rejected" {
				decision = "rejected"
			}
			endpoint := ts.URL + "/api/approvals/" + url.PathEscape(snapshot.SDK.Approval.ID) + "/decision"
			status, raw = doJSON(t, http.MethodPost, endpoint, map[string]string{"decision": decision})
			wantStatus := 200
			if mode == "cancelled" {
				wantStatus = 409
			}
			if status != wantStatus {
				t.Fatalf("decision: %d want %d: %s", status, wantStatus, raw)
			}
			status, raw = doJSON(t, http.MethodPost, endpoint, map[string]string{"decision": "rejected"})
			if status != 409 {
				t.Fatalf("stale decision changed result: %d %s", status, raw)
			}
			deadline = time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "failed"
			if mode == "approved" {
				want = "passed"
			}
			if mode == "cancelled" {
				want = "interrupted"
			}
			if !srv.DrainRuns(5*time.Second) || detail.Run.State != want {
				t.Fatalf("state: %s want %s: %s", detail.Run.State, want, raw)
			}
			data, _ = os.ReadFile(calls)
			wantCalls := "explore\n"
			if mode == "approved" {
				wantCalls += "review\n"
			}
			if string(data) != wantCalls {
				t.Fatalf("wrong continuation: %s", data)
			}
			if mode == "changed" && !strings.Contains(string(detail.Run.Doc), "reviewed document changed") {
				t.Fatalf("lost pin failure: %s", detail.Run.Doc)
			}
			_, inbox = doJSON(t, http.MethodGet, ts.URL+"/api/inbox", nil)
			var pending struct {
				Approvals []store.Record `json:"approvals"`
			}
			if err := json.Unmarshal(inbox, &pending); err != nil {
				t.Fatal(err)
			}
			for _, approval := range pending.Approvals {
				if approval.RunID == launch.RunID {
					t.Fatal("terminal run left a pending approval")
				}
			}
		})
	}
}
