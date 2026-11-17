// cmd_show.go implements the `show` verb: displays a single run with its per-node
// table and a one-line summary.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// reviewFinding is one independent-review finding with the level the SDK worker
// derived from the reviewer's own label.
type reviewFinding struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// reviewStep is one review step's recorded verdict as `show` reports it.
type reviewStep struct {
	Title    string
	Passed   bool
	Findings []string
	Details  []reviewFinding
}

// sdkReviewSteps reads the independent reviews a run recorded. A review that
// predates typed findings keeps its plain strings; nothing here assigns a level
// the reviewer did not label.
func sdkReviewSteps(doc []byte) []reviewStep {
	var checkpoint struct {
		SDK struct {
			Nodes []struct {
				ID             string          `json:"id"`
				Title          string          `json:"title"`
				Phase          string          `json:"phase"`
				Findings       []string        `json:"findings"`
				FindingDetails []reviewFinding `json:"findingDetails"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if len(doc) == 0 || json.Unmarshal(doc, &checkpoint) != nil {
		return nil
	}
	var out []reviewStep
	for _, node := range checkpoint.SDK.Nodes {
		if len(node.Findings) == 0 && len(node.FindingDetails) == 0 {
			continue
		}
		title := node.Title
		if title == "" {
			title = node.ID
		}
		out = append(out, reviewStep{Title: title, Passed: node.Phase == "passed", Findings: node.Findings, Details: node.FindingDetails})
	}
	return out
}

// reviewSummary is the one line a reader gets for a review. It counts CAVEATS,
// not findings: a passed review whose three findings all CONFIRMED a criterion
// was being reported as "passed · 3 caveats" (S-17). A review with no typed
// findings says how many findings it recorded and claims nothing about them.
func reviewSummary(step reviewStep) string {
	if len(step.Details) == 0 {
		return fmt.Sprintf("%s · %d %s recorded", verdict(step.Passed), len(step.Findings), plural(len(step.Findings), "finding", "findings"))
	}
	caveats := 0
	for _, detail := range step.Details {
		if detail.Level == "fail" {
			return "failed — " + detail.Text
		}
		if detail.Level == "caveat" {
			caveats++
		}
	}
	if caveats == 0 {
		return verdict(step.Passed) + ", no caveats"
	}
	return fmt.Sprintf("%s · %d %s", verdict(step.Passed), caveats, plural(caveats, "caveat", "caveats"))
}

func verdict(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// runDetail mirrors the API response shape for GET /api/runs/{id}.
type runDetail struct {
	Run   store.Run    `json:"run"`
	Nodes []store.Node `json:"nodes"`
}

// cmdShow fetches and displays a single run (run + nodes) from the API.
// Usage: hyperplane show <runId> [--json]
func cmdShow(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane show <runId> [--json]")
		return 2
	}
	runID := args[0]
	asJSON := false
	for _, a := range args[1:] {
		if a == "--json" {
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.get("/api/runs/" + runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "show: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "show: run %q not found\n", runID)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "show: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var detail runDetail
	if err := mustUnmarshal(body, &detail); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor
	r := detail.Run

	// One-line summary.
	fmt.Printf("run       %s\n", colorize(ansiBold, r.RunID, noColor))
	fmt.Printf("workflow  %s\n", r.DAG)
	fmt.Printf("state     %s\n", stateColor(r.State, noColor))
	if !r.StartedAt.IsZero() {
		fmt.Printf("start     %s\n", r.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !r.EndedAt.IsZero() {
		duration := r.EndedAt.Sub(r.StartedAt)
		fmt.Printf("end       %s  (%.1fs)\n", r.EndedAt.Format("2006-01-02 15:04:05"), duration.Seconds())
	}
	fmt.Println()

	// Per-node table.
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tKIND\tSTATE\tSCORE\tERROR")
	for _, n := range detail.Nodes {
		score := "-"
		if n.Score != nil {
			score = fmt.Sprintf("%.2f", *n.Score)
		}
		errStr := n.Error
		if len(errStr) > 60 {
			errStr = errStr[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			n.NodeID, n.Kind, stateColor(n.State, noColor), score, errStr)
	}
	_ = tw.Flush()

	// What the independent reviews said, with the reviewer's own levels kept
	// apart: a confirmation is not a caveat and a caveat is not a failure.
	for _, step := range sdkReviewSteps(r.Doc) {
		fmt.Println()
		fmt.Printf("review    %s: %s\n", step.Title, reviewSummary(step))
		if len(step.Details) == 0 {
			for _, finding := range step.Findings {
				fmt.Printf("          - %s\n", finding)
			}
			continue
		}
		for _, group := range []struct{ heading, level string }{{"Failed", "fail"}, {"Caveats", "caveat"}, {"Confirmed", "confirmation"}} {
			printed := false
			for _, detail := range step.Details {
				if detail.Level != group.level {
					continue
				}
				if !printed {
					fmt.Printf("          %s\n", group.heading)
					printed = true
				}
				fmt.Printf("          - %s\n", detail.Text)
			}
		}
	}

	if r.State == "passed" {
		return 0
	}
	return 1
}
