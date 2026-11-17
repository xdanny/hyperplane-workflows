package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComposeCLIReportsUnsupportedAndMalformedProposals(t *testing.T) {
	for _, tc := range []struct {
		body         string
		status, exit int
	}{
		{`{"workflow":{"kind":"nodes","name":"Options","nodes":[{"id":"one","title":"Explore","instruction":"Compare options"}]},"unsupported":[]}`, 200, 0},
		{`{"workflow":{"kind":"nodes","name":"Options","nodes":[{"id":"one","title":"Draft","instruction":"Draft email"}]},"unsupported":["Cannot send email"]}`, 200, 1},
		{`{"workflow":null,"unsupported":["Cannot send email"]}`, 200, 1},
		{`{"error":"SDK unavailable"}`, 502, 1},
		{`invalid`, 200, 1},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/compose" {
				t.Errorf("unexpected route: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
		for _, args := range [][]string{{"--brief", "Choose an idea"}, {"--brief", "Choose an idea", "--json"}} {
			if exit := cmdCompose(args, true); exit != tc.exit {
				t.Errorf("exit %d want %d", exit, tc.exit)
			}
		}
		server.Close()
	}
}
