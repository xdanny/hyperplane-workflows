package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLaneDecomposeCLIRequiresCommittedPlan(t *testing.T) {
	for _, tc := range []struct {
		body         string
		status, exit int
	}{
		{`{"laneId":"lane","ticketIds":["a","b"]}`, 201, 0},
		{`{"status":"decomposing"}`, 202, 1},
		{`{}`, 201, 1},
		{`{"laneId":"lane","ticketIds":[]}`, 201, 1},
		{`{"laneId":"lane","ticketIds":["a","a"]}`, 201, 1},
		{`{"error":"model failed"}`, 502, 1},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/lanes/decompose" || r.Method != http.MethodPost {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
		for _, args := range [][]string{{"--name", "Report"}, {"--name", "Report", "--json"}} {
			if exit := cmdLaneDecompose(args, true); exit != tc.exit {
				t.Errorf("response %s exit=%d want=%d", tc.body, exit, tc.exit)
			}
		}
		server.Close()
	}
}

func TestLaneDecomposeCLIRejectsConflictingOrUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"--name", "Report", "--unknown"}, {"--from-run", "source", "--concept", "override"}, {"--from-run", "source", "--project", "override"}, {"--name"}} {
		if exit := cmdLaneDecompose(args, true); exit != 2 {
			t.Errorf("%v: exit=%d", args, exit)
		}
	}
}

func TestWorkersCLIRequiresManagedRunAndRejectsRetiredModes(t *testing.T) {
	for _, args := range [][]string{{"--lane", "lane", "--count", "2"}, {"--lane", "lane", "--review"}, {"--lane", "lane", "--max-retries", "1"}, {"--lane", "lane", "--engine", "codex-local"}} {
		if exit := cmdWorkers(args, true); exit != 2 {
			t.Errorf("%v exit=%d", args, exit)
		}
	}
	for _, tc := range []struct {
		body         string
		status, exit int
	}{
		{`{"laneId":"lane","ticketId":"ticket","runId":"sdk-run"}`, 202, 0},
		{`{"started":true,"workers":1}`, 202, 1},
		{`{"error":"No ready task"}`, 409, 1},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/lanes/lane/work" {
				t.Errorf("route %s", r.URL.Path)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
		if exit := cmdWorkers([]string{"--lane", "lane", "--json"}, true); exit != tc.exit {
			t.Errorf("%s exit=%d", tc.body, exit)
		}
		server.Close()
	}
}
