package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWatchUsesCommittedSDKStateAcrossTransports(t *testing.T) {
	for _, transport := range []string{"SSE", "EOF", "poll"} {
		for _, state := range []string{"passed", "failed", "interrupted", "blocked"} {
			t.Run(transport+"/"+state, func(t *testing.T) {
				var terminal atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/runs/sdk-run/events/stream":
						terminal.Store(true)
						if transport == "poll" {
							http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						if transport == "SSE" {
							// SDK completion uses run.done for every terminal phase.
							fmt.Fprint(w, "data: {\"type\":\"run.done\",\"seq\":1}\n\n")
						}
					case "/api/runs/sdk-run/events":
						fmt.Fprint(w, `[{"type":"run.done","seq":1}]`)
					case "/api/runs/sdk-run":
						current := "running"
						if terminal.Load() {
							current = state
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]string{"runId": "sdk-run", "state": current}})
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
				want := 1
				if state == "passed" {
					want = 0
				}
				if got := cmdWatch([]string{"sdk-run"}, true); got != want {
					t.Fatalf("exit %d, want %d for committed %s", got, want, state)
				}
			})
		}
	}
}

func TestWatchStreamClosureFallsBackWithoutClaimingSuccess(t *testing.T) {
	var polled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/runs/sdk-run/events/stream":
			w.Header().Set("Content-Type", "text/event-stream")
		case "/api/runs/sdk-run/events":
			polled.Store(true)
			fmt.Fprint(w, `[{"type":"sdk.checkpoint","seq":9}]`)
		case "/api/runs/sdk-run":
			state := "running"
			if polled.Load() {
				state = "interrupted"
			}
			fmt.Fprintf(w, `{"run":{"runId":"sdk-run","state":%q}}`, state)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
	if got := cmdWatch([]string{"sdk-run"}, true); got != 1 || !polled.Load() {
		t.Fatalf("exit=%d polled=%v; EOF cannot establish success", got, polled.Load())
	}
}

func TestWatchCannotConfirmMalformedMissingOrForeignRun(t *testing.T) {
	for _, body := range []string{`{}`, `{"run":{"runId":"sdk-run","state":"unknown"}}`, `{"run":{"runId":"other","state":"passed"}}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/events") {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
			if got := cmdWatch([]string{"sdk-run"}, true); got != 1 {
				t.Fatalf("unconfirmed run returned exit %d", got)
			}
		})
	}
}
