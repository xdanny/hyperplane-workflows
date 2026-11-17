// cmd_cancel.go implements the `hyperplane cancel <runId>` verb.
// It sends POST /api/runs/{id}/cancel to mark a running run as interrupted.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/xdanny/hyperplane-workflows/internal/config"
)

// cmdCancel cancels an active run via the API.
// Usage: hyperplane cancel <runId> [--json]
func cmdCancel(args []string, globalNoColor bool) int {
	asJSON := false
	runID := ""
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			if runID == "" {
				runID = a
			}
		}
	}
	if runID == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane cancel <runId> [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.postJSON("/api/runs/"+runID+"/cancel", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cancel: %v\n", err)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if jsonErr := json.Unmarshal(body, &raw); jsonErr == nil {
			printJSON(raw)
		}
		if status == 200 {
			return 0
		}
		return 1
	}

	noColor := globalNoColor || cl.noColor

	switch status {
	case 200:
		fmt.Printf("%s cancelled run %s\n", colorize(ansiGreen, "✓", noColor), runID)
		return 0
	case 404:
		fmt.Fprintf(os.Stderr, "cancel: run %q not found\n", runID)
		return 1
	case 409:
		fmt.Fprintf(os.Stderr, "cancel: run %q is not currently running\n", runID)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "cancel: server returned %d: %s\n", status, body)
		return 1
	}
}
