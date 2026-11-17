package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestRunDetailRetainsSDKVerificationWithoutGraphProofProjection(t *testing.T) {
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	id := fmt.Sprintf("sdk-detail-%d", time.Now().UnixNano())
	checkpoint := json.RawMessage(`{"phase":"passed","passed":true,"nodes":[{"id":"write","phase":"passed","verification":"not_requested"},{"id":"review","phase":"passed"}]}`)
	doc, err := json.Marshal(map[string]any{"sdk": checkpoint})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateRun(t.Context(), store.Run{RunID: id, DAG: "node-workflow", State: "passed", Mode: "run", Options: json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Write and review","nodes":[{"id":"write","kind":"task","title":"Write","instruction":"Create a file"},{"id":"review","kind":"check","title":"Review","instruction":"Review the file"}]}}`), Doc: doc}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(t.Context(), id) })
	if err = st.UpsertNode(t.Context(), store.Node{RunID: id, NodeID: "delivery", Kind: "sdk", State: "passed", Doc: checkpoint}); err != nil {
		t.Fatal(err)
	}
	status, raw := doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+id, nil)
	if status != 200 {
		t.Fatalf("detail: %d %s", status, raw)
	}
	var detail map[string]json.RawMessage
	if err = json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if _, exists := detail["proofNodes"]; exists {
		t.Fatal("retired graph proof projection returned")
	}
	var run store.Run
	if err = json.Unmarshal(detail["run"], &run); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		SDK struct {
			Nodes []struct{ ID, Phase, Verification string }
		}
	}
	if err = json.Unmarshal(run.Doc, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SDK.Nodes) != 2 || snapshot.SDK.Nodes[0].Verification != "not_requested" || snapshot.SDK.Nodes[1].Phase != "passed" {
		t.Fatalf("SDK evidence rewritten: %s", run.Doc)
	}
	var nodes []store.Node
	if err = json.Unmarshal(detail["nodes"], &nodes); err != nil || len(nodes) != 1 || nodes[0].Kind != "sdk" {
		t.Fatalf("delivery lost: %s %v", detail["nodes"], err)
	}
	if _, ok := detail["outcome"]; !ok {
		t.Fatal("human outcome annotation removed")
	}
}
