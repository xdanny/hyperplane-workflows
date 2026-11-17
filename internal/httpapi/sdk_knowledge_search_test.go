package httpapi_test

import (
	"net/http"
	"testing"
)

func TestKnowledgeSearchRetiresGraphContextFilters(t *testing.T) {
	_, ts := newTestServer(t, nil)
	for _, query := range []string{"kinds=run_context", "tags=old-graph-fact", "kinds=skill,unknown"} {
		status, raw := doJSON(t, http.MethodGet, ts.URL+"/api/kb?"+query, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, status, raw)
		}
	}
	status, raw := doJSON(t, http.MethodGet, ts.URL+"/api/kb?kinds=skill,memory,decision,radar", nil)
	if status != http.StatusOK {
		t.Fatalf("supported search: %d %s", status, raw)
	}
}
