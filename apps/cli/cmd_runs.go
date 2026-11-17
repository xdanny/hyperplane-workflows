// Run listing, logs and human outcome decisions.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdRuns fetches and displays the run list from the running API server.
// Flags: --state X, --limit N, --json.
// Subcommand: `runs outcome <runId> --verdict V [--note "…"]`.
func cmdRuns(args []string, globalNoColor bool) int {
	if len(args) > 0 && args[0] == "outcome" {
		return cmdRunsOutcome(args[1:], globalNoColor)
	}
	if len(args) > 0 && args[0] == "log" {
		return cmdRunsLog(args[1:])
	}

	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(os.Stderr, "unknown runs subcommand %q\n", args[0])
		return 2
	}

	stateFilter := ""
	limit := "20"
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--state":
			if i+1 < len(args) {
				stateFilter = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				limit = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := fmt.Sprintf("/api/runs?limit=%s", limit)
	if stateFilter != "" {
		path += "&state=" + stateFilter
	}

	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runs: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "runs: server returned %d: %s\n", status, body)
		return 1
	}

	var runs []store.Run
	if err := mustUnmarshal(body, &runs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if asJSON {
		printJSON(runs)
		return 0
	}

	noColor := globalNoColor || cl.noColor
	if len(runs) == 0 {
		fmt.Println("no runs found")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tWORKFLOW\tSTATE\tUPDATED")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.RunID, r.DAG,
			stateColor(r.State, noColor),
			r.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	_ = tw.Flush()
	return 0
}

// cmdRunsLog prints one node's log — headless parity with the run inspector's
// log panel, so "what did that node actually print?" is answerable without the
// web app or knowing the run directory's file layout.
//
// Usage: hyperplane runs log <runId> <nodeId> [--tail N]
//
// The log comes back as text/plain and is written through verbatim, which keeps
// it pipeable into grep/less — the two tools that make a long log searchable at
// a terminal. `--tail N` is passed to the server so only the last N lines cross
// the wire.
func cmdRunsLog(args []string) int {
	const use = "usage: hyperplane runs log <runId> <nodeId> [--tail N]"
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	runID, nodeID := args[0], args[1]

	tail := ""
	for i := 2; i < len(args); i++ {
		switch {
		case args[i] == "--tail":
			if i+1 < len(args) {
				tail = args[i+1]
				i++
			}
		case strings.HasPrefix(args[i], "--tail="):
			tail = strings.TrimPrefix(args[i], "--tail=")
		}
	}
	if tail != "" {
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "runs log: --tail must be a positive integer")
			return 2
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := fmt.Sprintf("/api/runs/%s/log/%s",
		url.PathEscape(runID), url.PathEscape(nodeID))
	if tail != "" {
		path += "?tail=" + url.QueryEscape(tail)
	}

	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runs log: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "runs log: no log for node %q in run %s\n", nodeID, runID)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "runs log: server returned %d: %s\n", status, body)
		return 1
	}

	os.Stdout.Write(body)
	// Logs are written line-by-line, but a tail slice or a byte-capped response
	// can end mid-line; finish the line so the shell prompt starts clean.
	if len(body) > 0 && body[len(body)-1] != '\n' {
		fmt.Println()
	}
	return 0
}

// outcomeVerdicts are the three recordable Outcome Decisions, mirroring
// internal/httpapi. Accepting a shorthand ("changes") keeps the flag typable
// without loosening what actually reaches the API.
var outcomeVerdicts = map[string]string{
	"accepted":          "accepted",
	"accept":            "accepted",
	"changes_requested": "changes_requested",
	"changes":           "changes_requested",
	"rejected":          "rejected",
	"reject":            "rejected",
}

// cmdRunsOutcome records the human's final call on a finished run — headless
// parity with the Delivery view's Outcome Decision.
// Usage: hyperplane runs outcome <runId> --verdict accepted [--note "…"] [--json]
//
// An outcome is final: a second call surfaces the server's 409 rather than
// quietly overwriting the verdict already on record.
func cmdRunsOutcome(args []string, _ bool) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: hyperplane runs outcome <runId> --verdict accepted|changes_requested|rejected [--note \"…\"]")
		return 2
	}
	runID := args[0]
	verdictArg, note := "", ""
	asJSON := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--verdict":
			if i+1 < len(args) {
				verdictArg = args[i+1]
				i++
			}
		case "--note":
			if i+1 < len(args) {
				note = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		}
	}

	verdict, ok := outcomeVerdicts[strings.ToLower(strings.TrimSpace(verdictArg))]
	if !ok {
		fmt.Fprintln(os.Stderr, "runs outcome: --verdict must be accepted, changes_requested, or rejected")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.postJSON("/api/runs/"+runID+"/outcome",
		map[string]string{"verdict": verdict, "note": note})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runs outcome: %v\n", err)
		return 1
	}
	if status != 201 {
		fmt.Fprintf(os.Stderr, "runs outcome: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	fmt.Printf("outcome recorded: %s → %s\n", runID, verdict)
	if note != "" {
		fmt.Printf("  note: %s\n", note)
	}
	return 0
}
