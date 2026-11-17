// Package main — api.go provides a minimal HTTP client for the Hyperplane API,
// plus ANSI color helpers, tabwriter utilities, and isatty detection.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// apiClient is a thin wrapper around net/http for the Hyperplane API.
type apiClient struct {
	base    string
	http    *http.Client
	noColor bool
}

func newAPIClient(addr string) *apiClient {
	_, noColor := os.LookupEnv("NO_COLOR")
	return &apiClient{
		base:    "http://" + addr,
		http:    &http.Client{Timeout: 10 * time.Second},
		noColor: noColor,
	}
}

// engineCallTimeout is the client-side ceiling for endpoints that hold the
// connection open across a synchronous billed engine call — compose, skill
// extraction, persona review. The server bounds each codex call at six minutes;
// ten covers evidence assembly and a slow start without letting a wedged server
// hold the terminal forever.
const engineCallTimeout = 10 * time.Minute

// newEngineAPIClient is newAPIClient with the engine-call ceiling. Any command
// whose endpoint runs codex before answering must use this one: the default
// client hangs up after 10 seconds, which surfaces as "context deadline
// exceeded" in the terminal while the server keeps spending the billed call.
func newEngineAPIClient(addr string) *apiClient {
	cl := newAPIClient(addr)
	cl.http.Timeout = engineCallTimeout
	return cl
}

// get performs a GET request and returns the raw response body.
func (c *apiClient) get(path string) ([]byte, int, error) {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return nil, 0, fmt.Errorf("api unreachable (%s): %w — is the server running? try: go run ./apps/api", c.base+path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// getStream opens a GET and returns the *http.Response for streaming (caller must close Body).
func (c *apiClient) getStream(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api unreachable: %w", err)
	}
	return resp, nil
}

// postJSON performs a POST with a JSON body and returns the raw response body.
func (c *apiClient) postJSON(path string, payload any) ([]byte, int, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Post(c.base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, 0, fmt.Errorf("api unreachable (%s): %w — is the server running? try: go run ./apps/api", c.base+path, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	return body, resp.StatusCode, readErr
}

// patchJSON performs a PATCH with a JSON body and returns the raw response body.
// PATCH rather than PUT because every project field is optional: an absent field
// is left alone server-side, which is what lets one flag change one thing.
func (c *apiClient) patchJSON(path string, payload any) ([]byte, int, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPatch, c.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("api unreachable (%s): %w — is the server running? try: go run ./apps/api", c.base+path, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	return body, resp.StatusCode, readErr
}

// deleteReq performs a DELETE request and returns the raw response body.
func (c *apiClient) deleteReq(path string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodDelete, c.base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("api unreachable (%s): %w — is the server running? try: go run ./apps/api", c.base+path, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	return body, resp.StatusCode, readErr
}

// mustUnmarshal decodes JSON or fatally prints an error.
func mustUnmarshal(body []byte, dst any) error {
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("bad JSON from server: %w\nbody: %s", err, body)
	}
	return nil
}

// ---- ANSI color helpers ----

const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiCyan   = "\033[36m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
)

// colorize wraps s in the given ANSI code, unless color is disabled.
func colorize(code, s string, noColor bool) string {
	if noColor {
		return s
	}
	return code + s + ansiReset
}

// stateColor returns the ANSI color for a run/node state.
func stateColor(state string, noColor bool) string {
	switch state {
	case "passed":
		return colorize(ansiGreen, state, noColor)
	case "failed":
		return colorize(ansiRed, state, noColor)
	case "blocked", "running":
		return colorize(ansiYellow, state, noColor)
	case "skipped":
		return colorize(ansiDim, state, noColor)
	default:
		return state
	}
}

// levelColor returns the ANSI color for an event level.
func levelColor(level string, noColor bool) string {
	switch level {
	case "error":
		return colorize(ansiRed, level, noColor)
	case "warn":
		return colorize(ansiYellow, level, noColor)
	case "info":
		return colorize(ansiCyan, level, noColor)
	default:
		return level
	}
}

// printJSON emits raw JSON to stdout for machine consumption.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// truncate shortens s to maxLen with "…" suffix.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}
