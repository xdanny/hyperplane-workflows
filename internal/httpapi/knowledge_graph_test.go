package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestKnowledgeGraphRetainsSkillsAndDecisions(t *testing.T) {
	server := setupTestServer(t)
	st := openStore(t)
	defer st.Close()
	ctx := context.Background()
	id := "sdk-knowledge-" + t.Name()
	if err := st.UpsertSkill(ctx, store.Skill{ID: id, Name: id, Instruction: "Review the produced files", Category: "review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDecision(ctx, store.Decision{ID: id, Title: "Review files", Project: id, Decision: "Use " + id + " to review produced files"}); err != nil {
		t.Fatal(err)
	}
	body, status := getJSON(t, server.URL+"/api/graph/knowledge?project="+url.QueryEscape(id))
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var graph struct {
		Nodes  []struct{ ID, Kind string }
		Edges  []struct{ From, To, Kind string }
		Counts map[string]int
	}
	if err := json.Unmarshal(body, &graph); err != nil {
		t.Fatal(err)
	}
	foundDecision, foundSkill, foundLink := false, false, false
	for _, n := range graph.Nodes {
		if n.Kind == "process" {
			t.Fatalf("retired process in knowledge graph: %s", body)
		}
		foundDecision = foundDecision || n.ID == "decision:"+id
		foundSkill = foundSkill || n.ID == "skill:"+id
	}
	for _, e := range graph.Edges {
		foundLink = foundLink || e.From == "decision:"+id && e.To == "skill:"+id && e.Kind == "uses"
	}
	if !foundDecision || !foundSkill || !foundLink {
		t.Fatalf("lost knowledge relationships: %s", body)
	}
	if _, exists := graph.Counts["process"]; exists {
		t.Fatalf("retired process count: %s", body)
	}
}
