// cmd_watch.go implements the `watch` verb: streams live run events from the
// API's SSE endpoint (with polling fallback) until the run reaches a terminal state.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdWatch streams events for a run until it reaches a terminal state.
// Falls back to polling /api/runs/{id}/events?since= when SSE fails.
func cmdWatch(args []string, globalNoColor bool) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane watch <runId>")
		return 2
	}
	runID := args[0]

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	noColor := globalNoColor || cl.noColor

	fmt.Printf("watching run %s\n", colorize(ansiBold, runID, noColor))

	// Try SSE first.
	done, err := watchSSE(cl, runID, noColor)
	if err != nil {
		// SSE failed — fall back to polling.
		fmt.Fprintf(os.Stderr, "SSE unavailable (%v), falling back to polling\n", err)
		done, err = watchPoll(cl, runID, noColor)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: %v\n", err)
		return 1
	}

	if done == "passed" {
		return 0
	}
	return 1
}

// watchSSE connects to the SSE stream and prints events until run terminal state.
// Returns the final run state and any connection error.
func watchSSE(cl *apiClient, runID string, noColor bool) (string, error) {
	// The database record owns the result. run.done is also emitted for failed
	// and interrupted SDK runs; a closed stream says nothing about success.
	state, err := watchRunState(cl, runID)
	if err != nil || watchTerminal(state) {
		return state, err
	}
	resp, err := cl.getStream("/api/runs/" + url.PathEscape(runID) + "/events/stream")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SSE returned %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var e store.Event
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &e) != nil {
			continue
		}
		printEvent(e, noColor)
		if e.Type == "run.done" || e.Type == "run.fail" || e.Type == "run.block" {
			state, err := watchRunState(cl, runID)
			if err != nil || watchTerminal(state) {
				return state, err
			}
		}
	}
	// Even a truncated or failed stream can coincide with a committed terminal
	// result. Resolve it before falling back to polling.
	state, err = watchRunState(cl, runID)
	if err != nil || watchTerminal(state) {
		return state, err
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("event stream ended while run is %s", state)
}

// watchPoll reads events for display, and the saved run state for completion.
func watchPoll(cl *apiClient, runID string, noColor bool) (string, error) {
	var since int64
	for {
		path := fmt.Sprintf("/api/runs/%s/events?since=%d", url.PathEscape(runID), since)
		body, status, err := cl.get(path)
		if err != nil {
			return "", err
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("event polling returned %d", status)
		}
		var events []store.Event
		if err := json.Unmarshal(body, &events); err != nil {
			return "", fmt.Errorf("invalid event response: %w", err)
		}
		for _, e := range events {
			printEvent(e, noColor)
			if e.Seq > since {
				since = e.Seq
			}
		}
		state, err := watchRunState(cl, runID)
		if err != nil || watchTerminal(state) {
			return state, err
		}
		time.Sleep(time.Second)
	}
}

func watchRunState(cl *apiClient, runID string) (string, error) {
	body, status, err := cl.get("/api/runs/" + url.PathEscape(runID))
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("read run returned %d", status)
	}
	var response struct {
		Run store.Run `json:"run"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("invalid run response: %w", err)
	}
	if response.Run.RunID != runID {
		return "", fmt.Errorf("run response does not identify requested run")
	}
	state := response.Run.State
	if state != "running" && !watchTerminal(state) {
		return "", fmt.Errorf("unknown run state %q", state)
	}
	return state, nil
}

func watchTerminal(state string) bool {
	switch state {
	case "passed", "failed", "blocked", "interrupted":
		return true
	}
	return false
}

// printEvent prints a formatted event line to stdout.
func printEvent(e store.Event, noColor bool) {
	ts := e.TS.Format("15:04:05")
	lvl := levelColor(e.Level, noColor)
	node := ""
	if e.Node != "" {
		node = " [" + colorize(ansiCyan, e.Node, noColor) + "]"
	}
	fmt.Printf("%s %s%s %s\n", colorize(ansiDim, ts, noColor), lvl, node, e.Type)
}
