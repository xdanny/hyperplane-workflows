package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKDAGSaveRoundTripAndCycleRejection(t *testing.T) {
	_, ts := newTestServer(t, nil)
	node := func(id string, deps []string) sdkworkflow.Node {
		return sdkworkflow.Node{ID: id, Title: id, Instruction: "Write " + id, DependsOn: deps}
	}
	nodes := []sdkworkflow.Node{node("join", []string{"left", "right"}), node("left", []string{"root"}), node("root", []string{}), node("right", []string{"root"})}
	saved := store.SDKWorkflow{Name: "Branching research", Kind: "nodes", Instruction: "Compare independent perspectives", Nodes: nodes}
	raw, _ := json.Marshal(saved)
	resp, err := http.Post(ts.URL+"/api/workflows", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Nodes, nodes) {
		t.Fatalf("save changed dependency identity: %+v", saved.Nodes)
	}
	resp, err = http.Get(ts.URL + "/api/workflows")
	if err != nil {
		t.Fatal(err)
	}
	var library []store.SDKWorkflow
	err = json.NewDecoder(resp.Body).Decode(&library)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range library {
		if item.ID == saved.ID {
			found = true
			if !reflect.DeepEqual(item.Nodes, nodes) {
				t.Fatal("database lost dependency edges or explicit root")
			}
		}
	}
	if !found {
		t.Fatal("saved DAG missing")
	}
	saved.Nodes[2].DependsOn = []string{"join"}
	raw, _ = json.Marshal(saved)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/workflows/"+saved.ID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("cycle accepted: %d %s", resp.StatusCode, body)
	}
}
