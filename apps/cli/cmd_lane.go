// cmd_lane.go implements lane and ticket verbs for the managed SDK Board.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// decomposeResponse matches the JSON body returned by POST /api/lanes/decompose.
type decomposeResponse struct {
	LaneID    string   `json:"laneId"`
	TicketIDs []string `json:"ticketIds"`
}

// cmdLane dispatches `hyperplane lane <sub>` commands.
// Sub-commands: create, list, decompose
func cmdLane(args []string, noColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane lane <create|list|decompose> [flags]")
		return 2
	}
	switch args[0] {
	case "create":
		return cmdLaneCreate(args[1:], noColor)
	case "list":
		return cmdLaneList(args[1:], noColor)
	case "decompose":
		return cmdLaneDecompose(args[1:], noColor)
	default:
		fmt.Fprintf(os.Stderr, "unknown lane sub-command %q\n", args[0])
		return 2
	}
}

// cmdLaneDecompose implements `hyperplane lane decompose`.
//
// Mode 1 — from a completed SDK run:
//
//	--from-run <sdkRunId> --target <dir> [--engine codex-local]
//
// Mode 2 — from explicit flags:
//
//	--name <appName> --concept "..." --features "f1;f2;..." [--native "n1;n2;..."] --target <dir> [--engine codex-local]
func cmdLaneDecompose(args []string, noColor bool) int {
	fromRun := ""
	name := ""
	concept := ""
	featuresStr := ""
	nativeStr := ""
	target := ""
	engine := ""
	project := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from-run" && i+1 < len(args):
			fromRun = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--from-run="):
			fromRun = strings.TrimPrefix(args[i], "--from-run=")

		case args[i] == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--name="):
			name = strings.TrimPrefix(args[i], "--name=")

		case args[i] == "--concept" && i+1 < len(args):
			concept = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--concept="):
			concept = strings.TrimPrefix(args[i], "--concept=")

		case args[i] == "--features" && i+1 < len(args):
			featuresStr = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--features="):
			featuresStr = strings.TrimPrefix(args[i], "--features=")

		case args[i] == "--native" && i+1 < len(args):
			nativeStr = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--native="):
			nativeStr = strings.TrimPrefix(args[i], "--native=")

		case args[i] == "--target" && i+1 < len(args):
			target = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--target="):
			target = strings.TrimPrefix(args[i], "--target=")

		case args[i] == "--engine" && i+1 < len(args):
			engine = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--engine="):
			engine = strings.TrimPrefix(args[i], "--engine=")

		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")

		case args[i] == "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "lane decompose: unknown or incomplete argument %s\n", args[i])
			return 2
		}
	}

	// Validate: at least one of the two modes must be supplied.
	if fromRun == "" && name == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane lane decompose")
		fmt.Fprintln(os.Stderr, "  --from-run <runId> --target <dir> [--engine codex-local]")
		fmt.Fprintln(os.Stderr, "  --name <app> --concept <text> --features <f1;f2> [--native <n1;n2>] --target <dir> [--engine codex-local] [--project <p>]")
		return 2
	}
	if fromRun != "" && (name != "" || concept != "" || featuresStr != "" || nativeStr != "" || project != "") {
		fmt.Fprintln(os.Stderr, "lane decompose: --from-run supplies context and project; do not combine it with explicit planning fields")
		return 2
	}

	// Build the POST body.
	payload := map[string]any{
		"targetRoot": target,
	}
	if engine != "" {
		payload["engine"] = engine
	}
	// --project only applies to the explicit (--name) mode — a --from-run lane
	// always inherits its project from the source run, server-side.
	if project != "" {
		payload["project"] = project
	}
	if fromRun != "" {
		payload["fromRun"] = fromRun
	} else {
		payload["name"] = name
		if concept != "" {
			payload["concept"] = concept
		}
		if featuresStr != "" {
			var features []string
			for _, f := range strings.Split(featuresStr, ";") {
				f = strings.TrimSpace(f)
				if f != "" {
					features = append(features, f)
				}
			}
			payload["features"] = features
		}
		if nativeStr != "" {
			var native []string
			for _, n := range strings.Split(nativeStr, ";") {
				n = strings.TrimSpace(n)
				if n != "" {
					native = append(native, n)
				}
			}
			payload["nativeIntegrations"] = native
		}
	}

	cfg := config.Load()
	cl := newEngineAPIClient(cfg.Addr)

	body, status, err := cl.postJSON("/api/lanes/decompose", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lane decompose: %v\n", err)
		return 1
	}
	if status != http.StatusCreated {
		fmt.Fprintf(os.Stderr, "lane decompose: server returned %d: %s\n", status, body)
		return 1
	}

	var resp decomposeResponse
	if err := mustUnmarshal(body, &resp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if strings.TrimSpace(resp.LaneID) == "" || len(resp.TicketIDs) == 0 {
		fmt.Fprintln(os.Stderr, "lane decompose: server did not return a saved lane and tickets")
		return 1
	}
	seen := map[string]bool{}
	for _, id := range resp.TicketIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			fmt.Fprintln(os.Stderr, "lane decompose: invalid saved ticket identities")
			return 1
		}
		seen[id] = true
	}
	if asJSON {
		printJSON(resp)
		return 0
	}

	fmt.Printf("%s lane created: %s\n", colorize(ansiGreen, "✓", noColor), resp.LaneID)
	fmt.Printf("tickets (%d):\n", len(resp.TicketIDs))
	for i, id := range resp.TicketIDs {
		fmt.Printf("  [%d] %s\n", i+1, id)
	}
	fmt.Printf("\nRun: hyperplane workers --lane %s --count 1\n", resp.LaneID)
	return 0
}

// cmdLaneCreate creates a new swim-lane.
// hyperplane lane create --name <n> --desc <d> --target <path> [--project <p>]
func cmdLaneCreate(args []string, noColor bool) int {
	name := ""
	desc := ""
	target := ""
	project := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--name="):
			name = strings.TrimPrefix(args[i], "--name=")
		case args[i] == "--desc" && i+1 < len(args):
			desc = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--desc="):
			desc = strings.TrimPrefix(args[i], "--desc=")
		case args[i] == "--target" && i+1 < len(args):
			target = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--target="):
			target = strings.TrimPrefix(args[i], "--target=")
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--json":
			asJSON = true
		}
	}

	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane lane create --name <name> [--desc <desc>] [--target <path>] [--project <p>]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	payload := map[string]any{
		"name":        name,
		"description": desc,
		"targetRoot":  target,
		"project":     project,
	}
	body, status, err := cl.postJSON("/api/lanes", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lane create: %v\n", err)
		return 1
	}
	if status != 201 {
		fmt.Fprintf(os.Stderr, "lane create: server returned %d: %s\n", status, body)
		return 1
	}

	var lane store.Lane
	if err := mustUnmarshal(body, &lane); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(lane)
		return 0
	}
	fmt.Printf("%s lane created: %s (%s)\n", colorize(ansiGreen, "✓", noColor), lane.Name, lane.ID)
	return 0
}

// cmdLaneList lists lanes with ticket counts.
// hyperplane lane list [--project <p>]
func cmdLaneList(args []string, noColor bool) int {
	asJSON := false
	project := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--json":
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := "/api/lanes"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lane list: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "lane list: server returned %d: %s\n", status, body)
		return 1
	}

	var lanes []store.LaneWithCounts
	if err := mustUnmarshal(body, &lanes); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(lanes)
		return 0
	}

	if len(lanes) == 0 {
		fmt.Println("no lanes found")
		return 0
	}

	_ = noColor
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPROJECT\tSTATUS\ttodo\tip\treview\tdone\tblocked")
	for _, l := range lanes {
		proj := l.Project
		if proj == "" {
			proj = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n",
			l.ID, l.Name, proj, l.Status,
			l.Counts.Todo, l.Counts.InProgress,
			l.Counts.Review, l.Counts.Done, l.Counts.Blocked)
	}
	_ = tw.Flush()
	return 0
}

// cmdTicket dispatches `hyperplane ticket <sub>` commands.
// Sub-commands: add, list
func cmdTicket(args []string, noColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane ticket <add|list> [flags]")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdTicketAdd(args[1:], noColor)
	case "list":
		return cmdTicketList(args[1:], noColor)
	default:
		fmt.Fprintf(os.Stderr, "unknown ticket sub-command %q\n", args[0])
		return 2
	}
}

// cmdTicketAdd creates a ticket in a lane.
// hyperplane ticket add --lane <id> --title <t> --detail <d> [--priority N] [--deps id,id]
func cmdTicketAdd(args []string, noColor bool) int {
	laneID := ""
	title := ""
	detail := ""
	priority := 2
	depsStr := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--lane" && i+1 < len(args):
			laneID = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--lane="):
			laneID = strings.TrimPrefix(args[i], "--lane=")
		case args[i] == "--title" && i+1 < len(args):
			title = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--title="):
			title = strings.TrimPrefix(args[i], "--title=")
		case args[i] == "--detail" && i+1 < len(args):
			detail = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--detail="):
			detail = strings.TrimPrefix(args[i], "--detail=")
		case args[i] == "--priority" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &priority)
			i++
		case strings.HasPrefix(args[i], "--priority="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--priority="), "%d", &priority)
		case args[i] == "--deps" && i+1 < len(args):
			depsStr = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--deps="):
			depsStr = strings.TrimPrefix(args[i], "--deps=")
		case args[i] == "--json":
			asJSON = true
		}
	}

	if laneID == "" || title == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane ticket add --lane <id> --title <title> [--detail <d>] [--priority N] [--deps id,id]")
		return 2
	}

	var deps []string
	if depsStr != "" {
		for _, d := range strings.Split(depsStr, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				deps = append(deps, d)
			}
		}
	}
	if deps == nil {
		deps = []string{}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	payload := map[string]any{
		"laneId":   laneID,
		"title":    title,
		"detail":   detail,
		"priority": priority,
		"deps":     deps,
	}
	body, status, err := cl.postJSON("/api/tickets", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket add: %v\n", err)
		return 1
	}
	if status != 201 {
		fmt.Fprintf(os.Stderr, "ticket add: server returned %d: %s\n", status, body)
		return 1
	}

	var t store.Ticket
	if err := mustUnmarshal(body, &t); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(t)
		return 0
	}
	fmt.Printf("%s ticket created: %s (%s)\n", colorize(ansiGreen, "✓", noColor), t.Title, t.ID)
	return 0
}

// cmdTicketList lists tickets in a lane.
// hyperplane ticket list --lane <id>
func cmdTicketList(args []string, noColor bool) int {
	laneID := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--lane" && i+1 < len(args):
			laneID = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--lane="):
			laneID = strings.TrimPrefix(args[i], "--lane=")
		case args[i] == "--json":
			asJSON = true
		}
	}

	if laneID == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane ticket list --lane <id>")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.get("/api/lanes/" + laneID + "/tickets")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket list: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "ticket list: server returned %d: %s\n", status, body)
		return 1
	}

	var tix []store.Ticket
	if err := mustUnmarshal(body, &tix); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(tix)
		return 0
	}

	if len(tix) == 0 {
		fmt.Println("no tickets in lane")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tSTATUS\tPRIORITY\tASSIGNEE")
	for _, t := range tix {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			t.ID, t.Title, t.Status, t.Priority, t.Assignee)
	}
	_ = tw.Flush()
	return 0
}

// cmdWorkers admits the next ready Board task and prints its managed SDK run.
func cmdWorkers(args []string, noColor bool) int {
	laneID := ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--lane" && i+1 < len(args):
			laneID = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--lane="):
			laneID = strings.TrimPrefix(args[i], "--lane=")
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--count" && i+1 < len(args) && args[i+1] == "1":
			i++
		case args[i] == "--count=1":
		default:
			fmt.Fprintln(os.Stderr, "workers: run one SDK task for human review; use --lane <id> [--json]. Parallel, engine, automatic review and retry flags are unsupported")
			return 2
		}
	}
	if strings.TrimSpace(laneID) == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane workers --lane <id> [--json] — run the next ready task for human review")
		return 2
	}
	cfg := config.Load()
	cl := newEngineAPIClient(cfg.Addr)
	body, status, err := cl.postJSON("/api/lanes/"+url.PathEscape(laneID)+"/work", map[string]int{"count": 1})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workers: %v\n", err)
		return 1
	}
	if status != http.StatusAccepted {
		fmt.Fprintf(os.Stderr, "workers: server returned %d: %s\n", status, body)
		return 1
	}
	var result struct {
		RunID    string `json:"runId"`
		TicketID string `json:"ticketId"`
		LaneID   string `json:"laneId"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.RunID == "" || result.TicketID == "" || result.LaneID != laneID {
		fmt.Fprintln(os.Stderr, "workers: server did not return an admitted task and SDK run")
		return 1
	}
	if asJSON {
		printJSON(result)
		return 0
	}
	fmt.Printf("%s task started: %s\nRun: %s\nInspect with: hyperplane show %s\nCancel with: hyperplane cancel %s\nCompletion waits for your review on the Board.\n", colorize(ansiGreen, "✓", noColor), result.TicketID, result.RunID, result.RunID, result.RunID)
	return 0
}
