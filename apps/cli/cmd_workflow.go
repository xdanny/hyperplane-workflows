// cmd_workflow.go implements the `workflow` sub-verb group: the CLI face of a
// SAVED workflow — the thing a person keeps and runs again, with this run's
// values supplied on the command line instead of edited into its prose.
//
//	hyperplane workflow list [--project p] [--json]
//	hyperplane workflow show <id|name> [--json]
//	hyperplane workflow run  <id|name> [--input k=v ...] [--project p] [--watch] [--json]
//	hyperplane workflow runs <id|name> [--limit N] [--json]
//	hyperplane workflow attach <id|name> [--run <runId> ...] [--json]
//
// `run` is also the answer to "can I put this on a schedule?": Hyperplane has
// no scheduler, and this verb is what a cron or launchd entry calls.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const workflowUse = "usage: hyperplane workflow <list|show|run|runs|attach> [args]"

func cmdWorkflow(args []string, noColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workflowUse)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return cmdWorkflowList(rest, noColor)
	case "show":
		return cmdWorkflowShow(rest, noColor)
	case "run":
		return cmdWorkflowRun(rest, noColor)
	case "runs":
		return cmdWorkflowRuns(rest, noColor)
	case "attach":
		return cmdWorkflowAttach(rest, noColor)
	default:
		fmt.Fprintf(os.Stderr, "unknown workflow sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, workflowUse)
		return 2
	}
}

// fetchWorkflows reads every saved workflow from the API.
func fetchWorkflows(cl *apiClient) ([]store.SDKWorkflow, error) {
	body, status, err := cl.get("/api/workflows")
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("server returned %d: %s", status, body)
	}
	var out []store.SDKWorkflow
	if err := mustUnmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// findWorkflow resolves a saved workflow by exact id, then exact name, then a
// unique case-insensitive name. An ambiguous name is an error rather than a
// guess: running the wrong workflow is not a recoverable mistake.
func findWorkflow(items []store.SDKWorkflow, ref string) (store.SDKWorkflow, error) {
	for _, w := range items {
		if w.ID == ref {
			return w, nil
		}
	}
	for _, w := range items {
		if w.Name == ref {
			return w, nil
		}
	}
	var matches []store.SDKWorkflow
	for _, w := range items {
		if strings.EqualFold(w.Name, ref) {
			matches = append(matches, w)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, w := range matches {
			names = append(names, w.ID)
		}
		return store.SDKWorkflow{}, fmt.Errorf("%q matches more than one saved workflow; use an id: %s", ref, strings.Join(names, ", "))
	}
	return store.SDKWorkflow{}, fmt.Errorf("no saved workflow %q; run `hyperplane workflow list`", ref)
}

func cmdWorkflowList(args []string, globalNoColor bool) int {
	asJSON, project := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--project":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: hyperplane workflow list [--project <id>] [--json]")
				return 2
			}
			project = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			fmt.Fprintln(os.Stderr, "usage: hyperplane workflow list [--project <id>] [--json]")
			return 2
		}
	}
	cl := newAPIClient(config.Load().Addr)
	items, err := fetchWorkflows(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow list: %v\n", err)
		return 1
	}
	if project != "" {
		var scoped []store.SDKWorkflow
		for _, w := range items {
			if w.Project == project {
				scoped = append(scoped, w)
			}
		}
		items = scoped
	}
	if asJSON {
		printJSON(items)
		return 0
	}
	if len(items) == 0 {
		fmt.Println("no saved workflows yet")
		return 0
	}
	noColor := globalNoColor || cl.noColor
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tSTEPS\tINPUTS\tPROJECT")
	for _, w := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", w.ID, colorize(ansiBold, w.Name, noColor), w.Kind, len(w.Nodes), len(w.Inputs), w.Project)
	}
	_ = tw.Flush()
	return 0
}

func cmdWorkflowShow(args []string, globalNoColor bool) int {
	const use = "usage: hyperplane workflow show <id|name> [--json]"
	ref, asJSON := "", false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if strings.HasPrefix(a, "--") {
			fmt.Fprintf(os.Stderr, "unknown flag %q\n%s\n", a, use)
			return 2
		} else if ref == "" {
			ref = a
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	cl := newAPIClient(config.Load().Addr)
	items, err := fetchWorkflows(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow show: %v\n", err)
		return 1
	}
	w, err := findWorkflow(items, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow show: %v\n", err)
		return 1
	}
	history, herr := fetchWorkflowRuns(cl, w.ID, 5)
	if asJSON {
		out := map[string]any{"workflow": w}
		if herr == nil {
			out["runs"], out["total"] = history.Runs, history.Total
		}
		printJSON(out)
		return 0
	}
	noColor := globalNoColor || cl.noColor
	fmt.Printf("id    %s\n", w.ID)
	fmt.Printf("name  %s\n", colorize(ansiBold, w.Name, noColor))
	fmt.Printf("kind  %s (revision %d)\n", w.Kind, w.Revision)
	if w.Project != "" {
		fmt.Printf("scope %s\n", w.Project)
	}
	fmt.Printf("goal  %s\n", w.Instruction)
	fmt.Printf("sign-off  %s\n", signOffSentence(w.SignOff))

	fmt.Println()
	if len(w.Inputs) == 0 {
		fmt.Println("This workflow asks for nothing each run.")
	} else {
		fmt.Println(colorize(ansiBold, "Inputs this workflow asks for each run", noColor))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  INPUT\tREQUIRED\tLABEL\tHINT")
		for _, in := range w.Inputs {
			required := "optional"
			if in.Required {
				required = colorize(ansiYellow, "required", noColor)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", in.ID, required, in.Label, in.Hint)
		}
		_ = tw.Flush()
		for _, in := range w.Inputs {
			if in.Example != "" {
				fmt.Printf("  e.g. --input %s=%q\n", in.ID, in.Example)
			}
		}
	}

	if len(w.Nodes) > 0 {
		fmt.Println()
		fmt.Println(colorize(ansiBold, "Steps", noColor))
		for i, node := range w.Nodes {
			kind := node.Kind
			if kind == "" {
				kind = "document"
			}
			fmt.Printf("  %d. %s (%s)\n", i+1, node.Title, kind)
		}
	}

	if herr == nil && len(history.Runs) > 0 {
		fmt.Println()
		fmt.Printf("%s (%d in total)\n", colorize(ansiBold, "Last runs", noColor), history.Total)
		printWorkflowRuns(history.Runs, noColor)
	}
	return 0
}

// workflowHistoryRun is one row of a saved workflow's history.
type workflowHistoryRun struct {
	RunID            string            `json:"runId"`
	RunNumber        int               `json:"runNumber"`
	StartedAt        time.Time         `json:"startedAt"`
	DurationMs       *int64            `json:"durationMs"`
	State            string            `json:"state"`
	WaitingForPerson bool              `json:"waitingForPerson"`
	Slots            map[string]string `json:"slots"`
	SlotLabels       map[string]string `json:"slotLabels"`
	ChecksDeclared   int               `json:"checksDeclared"`
	ChecksPassed     int               `json:"checksPassed"`
	FailingStep      *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
	} `json:"failingStep"`
	Outcome *struct {
		Verdict   string `json:"verdict"`
		DecidedBy string `json:"decidedBy"`
	} `json:"outcome"`
}

// workflowHistory is the read model behind GET /api/workflows/{id}/runs.
type workflowHistory struct {
	Workflow   store.SDKWorkflow    `json:"workflow"`
	Total      int                  `json:"total"`
	Runs       []workflowHistoryRun `json:"runs"`
	Attachable []workflowAttachable `json:"attachable"`
}

// workflowAttachable is one earlier run that ran these steps with no saved
// workflow of its own.
type workflowAttachable struct {
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
	State     string    `json:"state"`
	Reason    string    `json:"reason"`
}

func fetchWorkflowRuns(cl *apiClient, id string, limit int) (workflowHistory, error) {
	var out workflowHistory
	body, status, err := cl.get("/api/workflows/" + url.PathEscape(id) + "/runs?limit=" + strconv.Itoa(limit))
	if err != nil {
		return out, err
	}
	if status != 200 {
		return out, fmt.Errorf("server returned %d: %s", status, body)
	}
	return out, mustUnmarshal(body, &out)
}

// printWorkflowRuns writes the history table. A run is identified by its number
// within this workflow, not by an id tail, and the failing STEP is named.
func printWorkflowRuns(runs []workflowHistoryRun, noColor bool) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  RUN\tSTARTED\tDURATION\tSTATE\tCHECKS\tDECIDED\tINPUTS\tRUN ID")
	for _, r := range runs {
		duration := "\u2014"
		if r.DurationMs != nil {
			duration = (time.Duration(*r.DurationMs) * time.Millisecond).Round(time.Second).String()
		}
		state := r.State
		if r.WaitingForPerson {
			state = "waiting for you"
		} else if r.FailingStep != nil {
			state = r.State + " at " + r.FailingStep.Title
		}
		decided := "\u2014"
		if r.Outcome != nil {
			decided = r.Outcome.Verdict
			// An acceptance the platform recorded itself is never presented as
			// somebody's signature.
			if r.Outcome.DecidedBy == store.DecidedByAuto {
				decided += " (automatic)"
			}
		}
		inputs := []string{}
		for _, key := range sortedSlotKeys(r.Slots) {
			name := key
			if label := r.SlotLabels[key]; label != "" {
				name = label
			}
			inputs = append(inputs, name+"="+truncate(r.Slots[key], 24))
		}
		fmt.Fprintf(tw, "  #%d\t%s\t%s\t%s\t%d/%d\t%s\t%s\t%s\n", r.RunNumber,
			r.StartedAt.Local().Format("2006-01-02 15:04"), duration, stateColor(state, noColor),
			r.ChecksPassed, r.ChecksDeclared, decided, strings.Join(inputs, " "), r.RunID)
	}
	_ = tw.Flush()
}

func cmdWorkflowRuns(args []string, globalNoColor bool) int {
	const use = "usage: hyperplane workflow runs <id|name> [--limit N] [--json]"
	ref, asJSON, limit := "", false, 20
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, use)
				return 2
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "--limit needs a positive number\n%s\n", use)
				return 2
			}
			limit, i = n, i+1
		case strings.HasPrefix(args[i], "--"):
			fmt.Fprintf(os.Stderr, "unknown flag %q\n%s\n", args[i], use)
			return 2
		case ref == "":
			ref = args[i]
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	cl := newAPIClient(config.Load().Addr)
	items, err := fetchWorkflows(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow runs: %v\n", err)
		return 1
	}
	w, err := findWorkflow(items, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow runs: %v\n", err)
		return 1
	}
	history, err := fetchWorkflowRuns(cl, w.ID, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow runs: %v\n", err)
		return 1
	}
	if asJSON {
		printJSON(history)
		return 0
	}
	fmt.Printf("%s — %d run(s)\n", w.Name, history.Total)
	if len(history.Runs) == 0 {
		fmt.Println("no runs yet; start one with `hyperplane workflow run`")
		return 0
	}
	printWorkflowRuns(history.Runs, globalNoColor || cl.noColor)
	return 0
}

// cmdWorkflowRun launches a saved workflow with this run's values.
//
// Exit 2 on missing required inputs, with each one's hint printed: the point of
// declaring inputs is that a person can be told exactly what is missing instead
// of reading a rejected request.
func cmdWorkflowRun(args []string, globalNoColor bool) int {
	const use = "usage: hyperplane workflow run <id|name> [--input k=v ...] [--project <id>] [--watch] [--json]"
	ref, project := "", ""
	inputs := map[string]string{}
	asJSON, watch := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--watch":
			watch = true
		case args[i] == "--input":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, use)
				return 2
			}
			key, value, found := strings.Cut(args[i+1], "=")
			if !found || strings.TrimSpace(key) == "" {
				fmt.Fprintf(os.Stderr, "--input needs key=value, got %q\n%s\n", args[i+1], use)
				return 2
			}
			inputs[strings.TrimSpace(key)], i = value, i+1
		case args[i] == "--project":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, use)
				return 2
			}
			project, i = args[i+1], i+1
		case strings.HasPrefix(args[i], "--"):
			fmt.Fprintf(os.Stderr, "unknown flag %q\n%s\n", args[i], use)
			return 2
		case ref == "":
			ref = args[i]
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	cl := newAPIClient(config.Load().Addr)
	items, err := fetchWorkflows(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		return 1
	}
	w, err := findWorkflow(items, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		return 1
	}
	declared := map[string]bool{}
	for _, in := range w.Inputs {
		declared[in.ID] = true
	}
	for key := range inputs {
		if !declared[key] {
			fmt.Fprintf(os.Stderr, "workflow run: %q is not an input of %q; run `hyperplane workflow show %s`\n", key, w.Name, w.ID)
			return 2
		}
	}

	// The revision is the one just read: launching runs the definition this
	// command looked at, and a concurrent edit is reported rather than run.
	body, status, err := cl.postJSON("/api/run", map[string]any{
		"sourceWorkflowId": w.ID, "revision": w.Revision, "slots": inputs, "project": project,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		return 1
	}
	if status == 400 {
		var rejected struct {
			Error        string   `json:"error"`
			MissingSlots []string `json:"missingSlots"`
		}
		if json.Unmarshal(body, &rejected) == nil && len(rejected.MissingSlots) > 0 {
			fmt.Fprintf(os.Stderr, "workflow run: %s needs values it was not given:\n", w.Name)
			for _, id := range rejected.MissingSlots {
				fmt.Fprintf(os.Stderr, "  --input %s=…", id)
				for _, in := range w.Inputs {
					if in.ID != id {
						continue
					}
					if in.Label != "" {
						fmt.Fprintf(os.Stderr, "   %s", in.Label)
					}
					if in.Hint != "" {
						fmt.Fprintf(os.Stderr, "\n      %s", in.Hint)
					}
					if in.Example != "" {
						fmt.Fprintf(os.Stderr, "\n      e.g. %q", in.Example)
					}
				}
				fmt.Fprintln(os.Stderr)
			}
			return 2
		}
	}
	if status == 409 {
		fmt.Fprintf(os.Stderr, "workflow run: %q changed while this command read it; run it again\n", w.Name)
		return 1
	}
	if status != 202 {
		fmt.Fprintf(os.Stderr, "workflow run: server returned %d: %s\n", status, body)
		return 1
	}
	var launched struct {
		RunID string `json:"runId"`
	}
	if err := mustUnmarshal(body, &launched); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		return 1
	}
	if asJSON {
		printJSON(map[string]any{"runId": launched.RunID, "workflowId": w.ID, "workflowName": w.Name, "inputs": inputs})
	} else {
		fmt.Printf("started %s — run of %s\n", launched.RunID, colorize(ansiBold, w.Name, globalNoColor || cl.noColor))
	}
	if watch {
		return cmdWatch([]string{launched.RunID}, globalNoColor)
	}
	return 0
}

// signOffSentence says, in a sentence, what this workflow asks of a person when
// a run finishes. The policy word alone ("optional") reads as a setting; what a
// person needs is what will happen.
func signOffSentence(policy string) string {
	switch policy {
	case store.SignOffOptional:
		return "optional — finished runs are decidable but never queue for you"
	case store.SignOffAutoOnChecks:
		return "automatic when every declared check passes — other endings still ask you"
	default:
		return "required — every finished run waits for your decision"
	}
}

// cmdWorkflowAttach gives a saved workflow the earlier runs that already ran its
// steps. It moves identity only: each run keeps the definition it executed.
func cmdWorkflowAttach(args []string, globalNoColor bool) int {
	const use = "usage: hyperplane workflow attach <id|name> [--run <runId> ...] [--json]"
	ref, asJSON := "", false
	var runIDs []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--run":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "--run needs a run id\n%s\n", use)
				return 2
			}
			runIDs, i = append(runIDs, args[i+1]), i+1
		case strings.HasPrefix(args[i], "--"):
			fmt.Fprintf(os.Stderr, "unknown flag %q\n%s\n", args[i], use)
			return 2
		case ref == "":
			ref = args[i]
		default:
			fmt.Fprintf(os.Stderr, "unexpected argument %q\n%s\n", args[i], use)
			return 2
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	cl := newAPIClient(config.Load().Addr)
	items, err := fetchWorkflows(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow attach: %v\n", err)
		return 1
	}
	w, err := findWorkflow(items, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow attach: %v\n", err)
		return 1
	}
	noColor := globalNoColor || cl.noColor

	// With no run named, say which runs could be attached rather than guessing.
	if len(runIDs) == 0 {
		history, err := fetchWorkflowRuns(cl, w.ID, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflow attach: %v\n", err)
			return 1
		}
		if asJSON {
			printJSON(map[string]any{"attachable": history.Attachable})
			return 0
		}
		if len(history.Attachable) == 0 {
			fmt.Println("No earlier run ran these steps without a saved workflow of its own.")
			return 0
		}
		fmt.Printf("%s\n", colorize(ansiBold, "Earlier runs that ran these steps", noColor))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  STARTED\tSTATE\tRUN ID")
		for _, item := range history.Attachable {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", item.StartedAt.Local().Format("2006-01-02 15:04"),
				stateColor(item.State, noColor), item.RunID)
		}
		_ = tw.Flush()
		fmt.Printf("\nAttach them with: hyperplane workflow attach %s --run <runId>\n", ref)
		return 2
	}

	body, status, err := cl.postJSON("/api/workflows/"+url.PathEscape(w.ID)+"/attach-runs",
		map[string]any{"runIds": runIDs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow attach: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "workflow attach: server returned %d: %s\n", status, body)
		return 1
	}
	var result struct {
		Attached []string `json:"attached"`
		Skipped  []struct {
			RunID  string `json:"runId"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if err := mustUnmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "workflow attach: %v\n", err)
		return 1
	}
	if asJSON {
		printJSON(result)
		return len(result.Skipped)
	}
	for _, id := range result.Attached {
		fmt.Printf("attached %s to %s\n", id, w.Name)
	}
	for _, item := range result.Skipped {
		fmt.Fprintf(os.Stderr, "skipped %s: %s\n", item.RunID, item.Reason)
	}
	if len(result.Skipped) > 0 {
		return 1
	}
	return 0
}
