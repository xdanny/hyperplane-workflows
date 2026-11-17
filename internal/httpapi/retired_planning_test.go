package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Read storage directly: the retired draft API must not be used to prove that
// preparing SDK steps leaves graph storage untouched.
func storedDraftSnapshot(t *testing.T) []byte {
	t.Helper()
	st, err := store.Open(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var raw []byte
	err = st.Pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY id), '[]'::jsonb) FROM workflow_drafts d`).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func TestRetiredGraphPlanningEndpoints(t *testing.T) {
	_, ts := newTestServer(t, nil)
	before := storedDraftSnapshot(t)
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/plan-critic"},
		{http.MethodPost, "/api/drafts"},
		{http.MethodGet, "/api/drafts"},
		{http.MethodGet, "/api/drafts/old-draft"},
		{http.MethodDelete, "/api/drafts/old-draft"},
		{http.MethodPost, "/api/drafts/old-draft/promote"},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			status, body := doJSON(t, request.method, ts.URL+request.path, map[string]any{
				"name": "retired-plan", "spec": map[string]any{"name": "old", "nodes": []any{}},
			})
			if status != http.StatusNotFound {
				t.Fatalf("retired endpoint returned %d: %s", status, body)
			}
		})
	}
	if string(before) != string(storedDraftSnapshot(t)) {
		t.Fatal("retired planning endpoint changed graph draft storage")
	}
}
