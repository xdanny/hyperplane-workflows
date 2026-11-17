// cmd_template.go implements the `template` sub-verb group — the CLI face of
// recipes: workflow templates that declare what must be KNOWN (context slots)
// and what must be PROVEN (criteria).
//
//	hyperplane template list [--json]
//	hyperplane template show <id> [--json]
//	hyperplane template instantiate <id> [--slot k=v ...] [--run]
//	                                     [--project <name>] [--json]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdTemplate dispatches `hyperplane template <sub-verb>`.
func cmdTemplate(args []string, noColor bool) int {
	const use = "usage: hyperplane template <list|show|instantiate> [args]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return cmdTemplateList(rest, noColor)
	case "show":
		return cmdTemplateShow(rest, noColor)
	case "instantiate":
		return cmdTemplateInstantiate(rest, noColor)
	default:
		fmt.Fprintf(os.Stderr, "unknown template sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, use)
		return 2
	}
}

// fetchTemplates gets every workflow template from the API.
func fetchTemplates(cl *apiClient) ([]store.WorkflowTemplate, []byte, int, error) {
	body, status, err := cl.get("/api/templates")
	if err != nil || status != 200 {
		return nil, body, status, err
	}
	var tmpls []store.WorkflowTemplate
	if err := mustUnmarshal(body, &tmpls); err != nil {
		return nil, body, status, err
	}
	return tmpls, body, status, nil
}

// cmdTemplateList lists workflow templates, marking which ones are recipes.
// Usage: hyperplane template list [--json]
func cmdTemplateList(args []string, globalNoColor bool) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	tmpls, body, status, err := fetchTemplates(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template list: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "template list: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		printJSON(json.RawMessage(body))
		return 0
	}

	noColor := globalNoColor || cl.noColor
	if len(tmpls) == 0 {
		fmt.Println("no templates found — run `hyperplane seed` to load the defaults")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSHAPE\tSLOTS\tCRITERIA\tID")
	for _, t := range tmpls {
		shape := "template"
		if t.IsRecipe() {
			shape = colorize(ansiGreen, "recipe", noColor)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
			colorize(ansiBold, t.Name, noColor), shape,
			len(t.ContextSlots), len(t.Criteria), t.ID)
	}
	_ = tw.Flush()
	return 0
}

// cmdTemplateShow prints one template with its context slots and criteria.
// Usage: hyperplane template show <id|name> [--json]
func cmdTemplateShow(args []string, globalNoColor bool) int {
	var ref string
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if len(a) > 0 && a[0] != '-' && ref == "" {
			ref = a
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane template show <id|name> [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	tmpls, body, status, err := fetchTemplates(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template show: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "template show: server returned %d: %s\n", status, body)
		return 1
	}

	t, ok := findTemplate(tmpls, ref)
	if !ok {
		fmt.Fprintf(os.Stderr, "template %q not found\n", ref)
		return 1
	}

	if asJSON {
		out, err := json.Marshal(t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "template show: %v\n", err)
			return 1
		}
		printJSON(json.RawMessage(out))
		return 0
	}

	noColor := globalNoColor || cl.noColor
	fmt.Printf("id    %s\n", t.ID)
	fmt.Printf("name  %s\n", colorize(ansiBold, t.Name, noColor))
	if t.Description != "" {
		fmt.Printf("about %s\n", t.Description)
	}

	if len(t.ContextSlots) > 0 {
		fmt.Println()
		fmt.Println(colorize(ansiBold, "Context slots — what must be known", noColor))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  SLOT\tREQUIRED\tLABEL\tHINT")
		for _, s := range t.ContextSlots {
			req := "optional"
			if s.Required {
				req = colorize(ansiYellow, "required", noColor)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.ID, req, s.Label, s.Hint)
		}
		_ = tw.Flush()
		for _, s := range t.ContextSlots {
			if s.Example != "" {
				fmt.Printf("  e.g. --slot %s=%q\n", s.ID, s.Example)
			}
		}
	}

	if len(t.Criteria) > 0 {
		fmt.Println()
		fmt.Println(colorize(ansiBold, "Criteria — what this recipe will prove", noColor))
		for _, c := range t.Criteria {
			fmt.Printf("  ◈ %s  (%s)\n", c.Title, c.ID)
			if c.Hint != "" {
				fmt.Printf("      %s\n", c.Hint)
			}
		}
	}

	if len(t.ContextSlots) == 0 && len(t.Criteria) == 0 {
		fmt.Println("\nthis template is not recipe-shaped (no context slots, no criteria)")
	}
	return 0
}

// sortedSlotKeys returns map keys in a stable order so repeated runs print the
// same thing.
func sortedSlotKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findTemplate resolves a template by exact id or exact name.
func findTemplate(tmpls []store.WorkflowTemplate, ref string) (store.WorkflowTemplate, bool) {
	for _, t := range tmpls {
		if t.ID == ref {
			return t, true
		}
	}
	for _, t := range tmpls {
		if t.Name == ref {
			return t, true
		}
	}
	return store.WorkflowTemplate{}, false
}

// cmdTemplateInstantiate prepares an SDK workflow and optionally launches it.
// Usage: hyperplane template instantiate <id|name> [--slot k=v ...] [--run]
//
//	[--project <name>] [--json]
func cmdTemplateInstantiate(args []string, globalNoColor bool) int {
	const use = "usage: hyperplane template instantiate <id|name> [--slot k=v ...] [--run] [--goal <text>] [--project <name>] [--json]"
	var ref, project string
	slots := map[string]string{}
	asJSON, doRun := false, false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--slot" && i+1 < len(args):
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || strings.TrimSpace(k) == "" {
				fmt.Fprintf(os.Stderr, "invalid --slot %q: expected k=v\n", args[i+1])
				return 2
			}
			slots[strings.TrimSpace(k)] = v
			i++
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case args[i] == "--run":
			doRun = true
		case args[i] == "--json":
			asJSON = true
		case len(args[i]) > 0 && args[i][0] != '-' && ref == "":
			ref = args[i]
		default:
			fmt.Fprintln(os.Stderr, use)
			return 2
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	// Resolve name → id so `template instantiate market-research` works.
	id := ref
	if tmpls, _, status, err := fetchTemplates(cl); err == nil && status == 200 {
		if t, ok := findTemplate(tmpls, ref); ok {
			id = t.ID
		}
	}

	body, status, err := cl.postJSON("/api/templates/"+id+"/instantiate", map[string]any{
		"slots":   slots,
		"run":     doRun,
		"project": project,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "template instantiate: %v\n", err)
		return 1
	}

	noColor := globalNoColor || cl.noColor

	if status == 400 {
		// The interesting failure: the recipe does not know enough to start.
		var e struct {
			Error        string   `json:"error"`
			MissingSlots []string `json:"missingSlots"`
		}
		_ = json.Unmarshal(body, &e)
		if asJSON {
			printJSON(json.RawMessage(body))
			return 1
		}
		if len(e.MissingSlots) > 0 {
			fmt.Fprintf(os.Stderr, "%s missing required context slot(s):\n", colorize(ansiRed, "✗", noColor))
			for _, m := range e.MissingSlots {
				fmt.Fprintf(os.Stderr, "  --slot %s=…\n", m)
			}
			fmt.Fprintf(os.Stderr, "\nrun `hyperplane template show %s` to see what each slot wants\n", ref)
			return 1
		}
		fmt.Fprintf(os.Stderr, "template instantiate: %s\n", strings.TrimSpace(string(body)))
		return 1
	}
	if status != 201 {
		fmt.Fprintf(os.Stderr, "template instantiate: server returned %d: %s\n", status, body)
		return 1
	}

	var res struct {
		Workflow *store.SDKWorkflow `json:"workflow"`
		Recipe   string             `json:"recipe"`
		Slots    map[string]string  `json:"slots"`
		Criteria []store.Criterion  `json:"criteria"`
		RunID    string             `json:"runId"`
	}
	if err := mustUnmarshal(body, &res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if res.Workflow == nil {
		fmt.Fprintln(os.Stderr, "template response contains no SDK workflow")
		return 1
	}
	if asJSON {
		printJSON(json.RawMessage(body))
		return 0
	}
	fmt.Printf("%s SDK workflow prepared: %s\n", colorize(ansiGreen, "✓", noColor), res.Workflow.Name)
	fmt.Printf("\n%s\n", res.Workflow.Instruction)
	for i, node := range res.Workflow.Nodes {
		if node.Access != nil {
			fmt.Printf("Step %d allowed hosts: %s\nReadable inputs: %s\nConnection names: %s\nTemporary tool setup: %t\n", i+1, strings.Join(node.Access.Hosts, ", "), strings.Join(node.Access.ReadPaths, ", "), strings.Join(node.Access.Environment, ", "), node.Toolchain != nil && *node.Toolchain && node.SideEffect != nil && *node.SideEffect)
		}
		kind := node.Kind
		if kind == "" {
			kind = "document"
		}
		fmt.Printf("\n%d. %s (%s)\n%s\n", i+1, node.Title, kind, node.Instruction)
		if node.Acceptance != "" {
			fmt.Printf("Built-in acceptance: %s\n", node.Acceptance)
		}
		if node.Inputs != nil {
			if node.Inputs["dataset"] != "" {
				fmt.Printf("Source: %s\nKey columns: %s\n", node.Inputs["dataset"], node.Inputs["keyColumns"])
			} else {
				data, _ := json.MarshalIndent(node.Inputs, "", "  ")
				fmt.Printf("Reviewed source inputs:\n%s\n", data)
			}
		}
		if len(node.Check) > 0 {
			command, _ := json.Marshal(node.Check)
			fmt.Printf("Acceptance command: %s\n", command)
		}
	}
	if res.RunID == "" {
		fmt.Println("\nPrepared only; not saved or started. Review these steps, then repeat with --run to start. Use --json for the editable definition.")
	}
	fmt.Printf("  recipe  %s\n", res.Recipe)
	if len(res.Slots) > 0 {
		fmt.Println("  context")
		for _, k := range sortedSlotKeys(res.Slots) {
			fmt.Printf("    %s = %s\n", k, res.Slots[k])
		}
	}
	if len(res.Criteria) > 0 {
		fmt.Println("  will prove")
		for _, c := range res.Criteria {
			fmt.Printf("    ◈ %s\n", c.Title)
		}
	}
	if res.RunID != "" {
		fmt.Printf("  run     %s\n", colorize(ansiBold, res.RunID, noColor))
		fmt.Printf("\nwatch it: hyperplane watch %s\n", res.RunID)
	}
	return 0
}
