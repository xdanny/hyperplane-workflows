// cmd_journeys.go implements the `journeys` verb: the delivery's story across
// runs, and the one verdict it asks for.
//
// `hyperplane runs` answers "what ran?". This answers "what are we delivering,
// and where has each one got to?" — the question a flat run list can only be
// made to answer by following repeated attempts by eye.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/journey"
)

// cmdJourneys dispatches `hyperplane journeys [<sub>]`.
// Sub-commands: list (default), show, outcome
func cmdJourneys(args []string, noColor bool) int {
	if len(args) == 0 {
		return cmdJourneysList(nil, noColor)
	}
	switch args[0] {
	case "list":
		return cmdJourneysList(args[1:], noColor)
	case "show":
		return cmdJourneysShow(args[1:], noColor)
	case "outcome":
		return cmdJourneysOutcome(args[1:], noColor)
	default:
		// Bare flags mean "list with these flags".
		if strings.HasPrefix(args[0], "-") {
			return cmdJourneysList(args, noColor)
		}
		fmt.Fprintf(os.Stderr, "unknown journeys sub-command %q\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: hyperplane journeys [list|show|outcome] [flags]")
		return 2
	}
}

// cmdJourneysList lists deliveries, newest activity first.
// hyperplane journeys list [--project <id>] [--needs-decision] [--json]
func cmdJourneysList(args []string, noColor bool) int {
	project := ""
	hasProject := false
	onlyAsking := false
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			project, hasProject = args[i+1], true
			i++
		case strings.HasPrefix(args[i], "--project="):
			project, hasProject = strings.TrimPrefix(args[i], "--project="), true
		case args[i] == "--needs-decision":
			onlyAsking = true
		case args[i] == "--json":
			asJSON = true
		}
	}

	path := "/api/journeys"
	if hasProject {
		path += "?project=" + url.QueryEscape(project)
	}

	journeys, code := fetchJourneys(path)
	if code != 0 {
		return code
	}
	if onlyAsking {
		journeys = journey.NeedingDecision(journeys)
	}
	if asJSON {
		printJSON(journeys)
		return 0
	}
	if len(journeys) == 0 {
		fmt.Println("no deliveries in scope yet — run a workflow, or widen with --project")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tDELIVERY\tSTAGE\tEVIDENCE\tATTEMPTS\tCHECKS PASSED\tUPDATED")
	asking := 0
	for _, j := range journeys {
		if j.NeedsDecision {
			asking++
		}
		proven := "-"
		if j.Criteria > 0 {
			proven = fmt.Sprintf("%d/%d", j.Proven, j.Criteria)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			j.Key, truncate(j.Name, 40), stageColor(j.Stage, noColor), j.Maturity,
			j.AttemptCount, proven, j.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	_ = tw.Flush()
	if asking > 0 {
		fmt.Printf("\n%d waiting on your verdict — hyperplane journeys outcome <key> --verdict accepted\n", asking)
	}
	return 0
}

// cmdJourneysShow prints one delivery's chapters and its narrative.
// hyperplane journeys show <key> [--json]
func cmdJourneysShow(args []string, noColor bool) int {
	key := ""
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if !strings.HasPrefix(a, "--") && key == "" {
			key = a
		}
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane journeys show <key> [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	body, status, err := cl.get("/api/journeys/" + url.PathEscape(key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "journeys show: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "journeys show: no delivery %q — try: hyperplane journeys\n", key)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "journeys show: server returned %d: %s\n", status, body)
		return 1
	}

	var j journey.Journey
	if err := mustUnmarshal(body, &j); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(j)
		return 0
	}

	fmt.Printf("%s %s\n", colorize(ansiBold, j.Name, noColor), colorize(ansiDim, "("+j.Key+")", noColor))
	line := fmt.Sprintf("%s   %s   %d attempt(s)", stageColor(j.Stage, noColor), j.Maturity, j.AttemptCount)
	if j.Criteria > 0 {
		line += fmt.Sprintf("   %d/%d checks passed", j.Proven, j.Criteria)
	}
	fmt.Println(line)
	meta := j.DAG
	if j.Recipe != "" {
		meta += " · " + j.Recipe
	}
	if j.Project != "" {
		meta += " · " + j.Project
	}
	fmt.Println(colorize(ansiDim, meta, noColor))

	if j.Outcome != nil {
		fmt.Printf("\noutcome: %s (%s grain, %s)\n", j.Outcome.Verdict, j.Outcome.Scope,
			j.Outcome.DecidedAt.Local().Format("2006-01-02 15:04"))
		if j.Outcome.Note != "" {
			fmt.Printf("  %s\n", j.Outcome.Note)
		}
	} else if j.NeedsDecision {
		fmt.Printf("\n%s no verdict recorded — hyperplane journeys outcome %s --verdict accepted\n",
			colorize(ansiYellow, "!", noColor), j.Key)
	}

	fmt.Println("\nChapters (newest first):")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  #\tRUN\tSTATE\tCHECKS PASSED\tNOTE")
	for _, a := range j.Attempts {
		proven := "-"
		if a.Criteria > 0 {
			proven = fmt.Sprintf("%d/%d", a.Proven, a.Criteria)
		}
		var notes []string
		if a.ContinuedFrom != "" {
			notes = append(notes, "continued from "+a.ContinuedFrom)
		}
		if a.Superseded {
			notes = append(notes, "superseded")
		}
		if a.FailingNode != "" {
			notes = append(notes, "at "+a.FailingNode)
		}
		if a.Outcome != nil {
			notes = append(notes, a.Outcome.Verdict)
		}
		note := "-"
		if len(notes) > 0 {
			note = strings.Join(notes, ", ")
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n",
			a.Number, a.RunID, stateColor(a.State, noColor), proven, note)
	}
	_ = tw.Flush()

	if len(j.Timeline) > 0 {
		fmt.Println("\nWhat happened:")
		for _, e := range j.Timeline {
			fmt.Printf("  %s  %s\n", colorize(ansiDim, e.At.Local().Format("2006-01-02 15:04"), noColor), e.Text)
		}
	}
	return 0
}

// cmdJourneysOutcome records the verdict on a whole delivery.
// hyperplane journeys outcome <key> --verdict accepted|changes_requested|rejected [--note "…"] [--json]
func cmdJourneysOutcome(args []string, noColor bool) int {
	key := ""
	verdict := ""
	note := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--verdict" && i+1 < len(args):
			verdict = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--verdict="):
			verdict = strings.TrimPrefix(args[i], "--verdict=")
		case args[i] == "--note" && i+1 < len(args):
			note = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--note="):
			note = strings.TrimPrefix(args[i], "--note=")
		case args[i] == "--json":
			asJSON = true
		case !strings.HasPrefix(args[i], "-") && key == "":
			key = args[i]
		}
	}

	// Reuse the run-grain aliases so "reject" means the same thing at both grains.
	if v, ok := outcomeVerdicts[strings.ToLower(strings.TrimSpace(verdict))]; ok {
		verdict = v
	}
	if key == "" || verdict == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane journeys outcome <key> --verdict accepted|changes_requested|rejected [--note \"…\"] [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	viewed, status, err := cl.get("/api/journeys/" + url.PathEscape(key))
	if err != nil || status != 200 {
		fmt.Fprintf(os.Stderr, "journeys outcome: could not read delivery (%d): %s %v\n", status, viewed, err)
		return 1
	}
	var j journey.Journey
	if err := mustUnmarshal(viewed, &j); err != nil || len(j.Attempts) == 0 {
		fmt.Fprintln(os.Stderr, "journeys outcome: delivery has no current attempt")
		return 1
	}
	body, status, err := cl.postJSON("/api/journeys/"+url.PathEscape(key)+"/outcome",
		map[string]any{"runId": j.Attempts[0].RunID, "verdict": verdict, "note": note})
	if err != nil {
		fmt.Fprintf(os.Stderr, "journeys outcome: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "journeys outcome: no delivery %q — try: hyperplane journeys\n", key)
		return 1
	}
	if status != 201 {
		// 409 carries the server's sentence: already decided, or still running.
		fmt.Fprintf(os.Stderr, "journeys outcome: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		fmt.Println(string(body))
		return 0
	}
	fmt.Printf("%s delivery %s: %s\n", colorize(ansiGreen, "✓", noColor), key, verdict)
	fmt.Printf("Recorded for attempt %s and its existing history. A later attempt needs its own decision.\n", j.Attempts[0].RunID)
	return 0
}

// fetchJourneys GETs a journey list, returning a non-zero exit code on failure.
func fetchJourneys(path string) ([]journey.Journey, int) {
	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journeys: %v\n", err)
		return nil, 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "journeys: server returned %d: %s\n", status, body)
		return nil, 1
	}
	var journeys []journey.Journey
	if err := mustUnmarshal(body, &journeys); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 1
	}
	return journeys, 0
}

// stageColor paints a journey stage in the run-state palette it corresponds to,
// so a delivery reads at the same glance as the runs beneath it.
func stageColor(stage journey.Stage, noColor bool) string {
	switch stage {
	case journey.StageBuilding, journey.StageVerifying:
		return colorize(ansiBlue, string(stage), noColor)
	case journey.StageAccepted:
		return colorize(ansiGreen, string(stage), noColor)
	case journey.StageRejected:
		return colorize(ansiRed, string(stage), noColor)
	case journey.StageNeedsDecision, journey.StageChangesRequested:
		return colorize(ansiYellow, string(stage), noColor)
	default:
		return string(stage)
	}
}
