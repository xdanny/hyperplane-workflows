package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJourneyOutcomePinsAttemptAndDoesNotRetryStaleDecision(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			gets, posts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/journeys/delivery":
					gets++
					_, _ = w.Write([]byte(`{"attempts":[{"runId":"reviewed-run"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/journeys/delivery/outcome":
					posts++
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["runId"] != "reviewed-run" || body["verdict"] != "accepted" || body["note"] != "Reviewed the report" {
						t.Errorf("decision is not bound to reviewed attempt: %v", body)
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"key":"delivery"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
			code := cmdJourneysOutcome([]string{"delivery", "--verdict", "accepted", "--note", "Reviewed the report", "--json"}, true)
			wantCode := 0
			if status == http.StatusConflict {
				wantCode = 1
			}
			if code != wantCode || gets != 1 || posts != 1 {
				t.Fatalf("code=%d gets=%d posts=%d", code, gets, posts)
			}
		})
	}
}
