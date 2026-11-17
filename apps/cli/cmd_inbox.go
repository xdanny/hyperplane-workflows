// cmd_inbox.go implements inbox, approve, and select verbs for the human-in-the-loop
// workflow: pending approvals + selections, plus posting decisions.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdInbox fetches and displays pending approvals and selections from the API.
// Flags: --json.
func cmdInbox(args []string, globalNoColor bool) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.get("/api/inbox")
	if err != nil {
		fmt.Fprintf(os.Stderr, "inbox: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "inbox: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var inbox struct {
		Approvals  []store.Record `json:"approvals"`
		Selections []store.Record `json:"selections"`
	}
	if err := mustUnmarshal(body, &inbox); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor

	if len(inbox.Approvals) == 0 && len(inbox.Selections) == 0 {
		fmt.Println("inbox is empty")
		return 0
	}

	if len(inbox.Approvals) > 0 {
		fmt.Printf("%s\n", colorize(ansiBold, "Pending Approvals:", noColor))
		for _, a := range inbox.Approvals {
			printRecord("approval", a, noColor)
		}
	}
	if len(inbox.Selections) > 0 {
		fmt.Printf("%s\n", colorize(ansiBold, "Pending Selections:", noColor))
		for _, s := range inbox.Selections {
			printRecord("selection", s, noColor)
		}
	}
	return 0
}

// printRecord renders a single inbox record (approval or selection) for TTY output.
func printRecord(kind string, r store.Record, noColor bool) {
	fmt.Printf("  %s  %s  run=%s  gate=%s\n",
		colorize(ansiBold, r.ID, noColor),
		colorize(ansiYellow, kind, noColor),
		r.RunID,
		r.GateKey)

	// Extract and display prompt from Doc if present.
	if len(r.Doc) > 0 {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(r.Doc, &doc); err == nil {
			for _, key := range []string{"prompt", "question", "message", "label"} {
				if v, ok := doc[key]; ok {
					var s string
					if err := json.Unmarshal(v, &s); err == nil && s != "" {
						fmt.Printf("    %s\n", colorize(ansiDim, s, noColor))
					}
				}
			}
		}
	}
}

// cmdApprove posts an approve/reject decision to an approval gate.
// Usage: hyperplane approve <id> [--reject]
func cmdApprove(args []string, _ bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane approve <id> [--reject]")
		return 2
	}
	id := args[0]
	decision := "approved"
	for _, a := range args[1:] {
		if a == "--reject" {
			decision = "rejected"
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	payload := map[string]string{"decision": decision}
	body, status, err := cl.postJSON("/api/approvals/"+id+"/decision", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "approve: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "approve: server returned %d: %s\n", status, body)
		return 1
	}

	fmt.Printf("approval %s → %s\n", id, decision)
	return 0
}

// cmdSelect posts a selection decision to a selection gate.
// Usage: hyperplane select <id> <winner>
func cmdSelect(args []string, _ bool) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane select <id> <winner>")
		return 2
	}
	id := args[0]
	winner := args[1]

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	payload := map[string]string{"winner": winner}
	body, status, err := cl.postJSON("/api/selections/"+id+"/decision", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "select: server returned %d: %s\n", status, body)
		return 1
	}

	fmt.Printf("selection %s → winner: %s\n", id, winner)
	return 0
}
