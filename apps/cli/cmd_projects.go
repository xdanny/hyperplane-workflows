// cmd_projects.go implements the `projects` verb: the CRUD view of first-class
// projects (the row behind the project tag that runs, lanes and decisions carry).
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdProjects dispatches `hyperplane projects <sub>`.
// Sub-commands: list, create, show, declare
func cmdProjects(args []string, noColor bool) int {
	if len(args) == 0 {
		return cmdProjectsList(nil, noColor)
	}
	switch args[0] {
	case "list":
		return cmdProjectsList(args[1:], noColor)
	case "create":
		return cmdProjectsCreate(args[1:], noColor)
	case "show":
		return cmdProjectsShow(args[1:], noColor)
	case "declare":
		return cmdProjectsDeclare(args[1:], noColor)
	default:
		fmt.Fprintf(os.Stderr, "unknown projects sub-command %q\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: hyperplane projects <list|create|show|declare> [flags]")
		return 2
	}
}

// cmdProjectsList lists projects with their item counts.
// hyperplane projects list [--include-test] [--json]
func cmdProjectsList(args []string, noColor bool) int {
	asJSON := false
	includeTest := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--include-test":
			includeTest = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := "/api/projects"
	if includeTest {
		path += "?includeTest=true"
	}
	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects list: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "projects list: server returned %d: %s\n", status, body)
		return 1
	}

	var projects []store.ProjectCount
	if err := mustUnmarshal(body, &projects); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(projects)
		return 0
	}
	if len(projects) == 0 {
		fmt.Println("no projects yet — tag a run with --project or: hyperplane projects create --id <id>")
		return 0
	}

	_ = noColor
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\truns\tlanes\tdecisions\tworkflows\tLAST ACTIVITY")
	for _, p := range projects {
		id := p.Project
		if id == "" {
			id = "(unscoped)"
		}
		name := p.Name
		if name == "" {
			name = "-"
		}
		status := p.Status
		if status == "" {
			// A tag that has items but no row yet — say so rather than
			// implying it is a registered project.
			status = "unregistered"
		}
		last := "-"
		if p.LastActivity != nil {
			last = p.LastActivity.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
			id, name, status, p.Runs, p.Lanes, p.Decisions, p.WorkflowCount, last)
	}
	_ = tw.Flush()
	return 0
}

// cmdProjectsCreate registers a project.
//
// hyperplane projects create --id <id> [--name <n>] [--summary <s>]
//
//	[--owner <o>] [--declarations <json|@file>] [--json]
//
// `--summary` is the human-owned "what this project is for". There is no
// narrative flag: "what this project has built so far" is written from accepted
// deliveries and is never typed.
func cmdProjectsCreate(args []string, noColor bool) int {
	id := ""
	name := ""
	summary := ""
	owner := ""
	declarations := ""
	asJSON := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--id" && i+1 < len(args):
			id = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--id="):
			id = strings.TrimPrefix(args[i], "--id=")
		case args[i] == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--name="):
			name = strings.TrimPrefix(args[i], "--name=")
		case args[i] == "--summary" && i+1 < len(args):
			summary = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--summary="):
			summary = strings.TrimPrefix(args[i], "--summary=")
		case args[i] == "--owner" && i+1 < len(args):
			owner = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--owner="):
			owner = strings.TrimPrefix(args[i], "--owner=")
		case args[i] == "--declarations" && i+1 < len(args):
			declarations = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--declarations="):
			declarations = strings.TrimPrefix(args[i], "--declarations=")
		case args[i] == "--json":
			asJSON = true
		}
	}

	if id == "" && name == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane projects create --id <id> [--name <name>] [--summary <text>] [--owner <who>] [--declarations <json|@file>] [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	payload := map[string]any{"id": id, "name": name, "summary": summary, "owner": owner}
	if declarations != "" {
		doc, err := readDeclarations(declarations)
		if err != nil {
			fmt.Fprintf(os.Stderr, "projects create: %v\n", err)
			return 2
		}
		payload["declarations"] = doc
	}
	body, status, err := cl.postJSON("/api/projects", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects create: %v\n", err)
		return 1
	}
	if status == 409 {
		fmt.Fprintf(os.Stderr, "projects create: project %q already exists\n", id)
		return 1
	}
	if status != 201 {
		fmt.Fprintf(os.Stderr, "projects create: server returned %d: %s\n", status, body)
		return 1
	}

	var p store.Project
	if err := mustUnmarshal(body, &p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(p)
		return 0
	}
	fmt.Printf("%s project created: %s (%s)\n", colorize(ansiGreen, "✓", noColor), p.Name, p.ID)
	fmt.Println("Nothing is scoped to it yet. Tag work with it, e.g.:")
	fmt.Printf("  hyperplane lane create --name <lane> --project %s\n", p.ID)
	return 0
}

// cmdProjectsShow prints one project's counts and the workflows it has run.
// hyperplane projects show <projectId> [--json]
func cmdProjectsShow(args []string, noColor bool) int {
	id := ""
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if !strings.HasPrefix(a, "--") && id == "" {
			id = a
		}
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane projects show <projectId> [--json]")
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.get("/api/projects/" + url.PathEscape(id))
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects show: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "projects show: no project %q — try: hyperplane projects list\n", id)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "projects show: server returned %d: %s\n", status, body)
		return 1
	}

	var d store.ProjectDetail
	if err := mustUnmarshal(body, &d); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(d)
		return 0
	}

	name := d.Name
	if name == "" {
		name = d.Project
	}
	fmt.Printf("%s %s\n", colorize(ansiBold, name, noColor), colorize(ansiDim, "("+d.Project+")", noColor))
	// Two sentences, two authors, said as two things. `summary` is what the
	// owner said the project is for; `narrative` is what the platform found it
	// actually built. Printing one where the other belongs is the bug this
	// column split exists to end.
	fmt.Println(colorize(ansiDim, "what this project is for:", noColor))
	if d.Summary != "" {
		fmt.Println("  " + d.Summary)
	} else {
		fmt.Println("  no description yet")
	}
	fmt.Println(colorize(ansiDim, "what this project has built so far:", noColor))
	if d.Narrative != "" {
		fmt.Println("  " + d.Narrative)
	} else {
		fmt.Println("  nothing has been accepted under this project yet")
	}
	if decl := strings.TrimSpace(string(d.Declarations)); decl != "" && decl != "{}" {
		parsed, perr := store.ParseDeclarations(d.Declarations)
		if perr == nil {
			fmt.Println(colorize(ansiDim, "declared:", noColor))
			if parsed.Purpose.Owner != "" {
				fmt.Printf("  owner: %s\n", parsed.Purpose.Owner)
			}
			if parsed.Purpose.Due != "" {
				fmt.Printf("  due: %s\n", parsed.Purpose.Due)
			}
			for _, ds := range parsed.Datasets {
				line := ds.Name
				if ds.Location != "" {
					line += " (" + ds.Location + ")"
				}
				if keys := store.DatasetKeyValue(ds); keys != "" {
					line += " — key: " + keys
				}
				fmt.Printf("  data: %s\n", strings.TrimSpace(line))
			}
			if parsed.Environment.Where != "" {
				fmt.Printf("  runs on: %s\n", parsed.Environment.Where)
			}
		}
	}
	if !d.Registered {
		fmt.Println("This project exists only as a tag — it has no project row yet.")
	}
	fmt.Printf("\nruns %d   lanes %d   decisions %d   workflows %d\n",
		d.Runs, d.Lanes, d.Decisions, d.WorkflowCount)
	if d.LastActivity != nil {
		fmt.Printf("last activity %s\n", d.LastActivity.Local().Format("2006-01-02 15:04"))
	}

	if len(d.Workflows) == 0 {
		fmt.Println("\nNo workflows have run under this project yet.")
		return 0
	}
	fmt.Println("\nWorkflows (derived from the runs that carry this project):")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  WORKFLOW\tRECIPE\truns\tLAST RUN\tSTATE")
	for _, wf := range d.Workflows {
		// The recipe's display name, never its uuid — the id is a join key,
		// not something a reader can act on.
		recipe := wf.RecipeName
		if recipe == "" {
			recipe = wf.Recipe
		}
		if recipe == "" {
			recipe = "-"
		}
		last := "-"
		if wf.LastRunAt != nil {
			last = wf.LastRunAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n", wf.Name, recipe, wf.Runs, last, stateColor(wf.LastState, noColor))
	}
	_ = tw.Flush()
	return 0
}

// cmdProjectsDeclare replaces the named groups of a project's declaration
// document: what it is for, the data it works on, where its runs execute.
//
// hyperplane projects declare <projectId> --declarations <json|@file> [--json]
//
// The server MERGES group by group, so a document naming only `datasets` leaves
// every other group — including one this build has never heard of — exactly as
// it was. That is what keeps the document extensible.
func cmdProjectsDeclare(args []string, noColor bool) int {
	id := ""
	declarations := ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--declarations" && i+1 < len(args):
			declarations = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--declarations="):
			declarations = strings.TrimPrefix(args[i], "--declarations=")
		case args[i] == "--json":
			asJSON = true
		case !strings.HasPrefix(args[i], "--") && id == "":
			id = args[i]
		}
	}
	if id == "" || declarations == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane projects declare <projectId> --declarations <json|@file> [--json]")
		return 2
	}
	doc, err := readDeclarations(declarations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects declare: %v\n", err)
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	body, status, err := cl.patchJSON("/api/projects/"+url.PathEscape(id),
		map[string]any{"declarations": doc})
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects declare: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "projects declare: no project %q — try: hyperplane projects list\n", id)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "projects declare: server returned %d: %s\n", status, body)
		return 1
	}
	var p store.Project
	if err := mustUnmarshal(body, &p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		printJSON(p)
		return 0
	}
	fmt.Printf("%s declarations updated for %s\n", colorize(ansiGreen, "✓", noColor), p.ID)
	fmt.Printf("Recipes run under this project now offer them as defaults: hyperplane projects show %s\n", p.ID)
	return 0
}

// readDeclarations reads a declaration document from a literal JSON argument or
// from @path. It is parsed here rather than passed through as a string so a
// typo is a clear local error instead of a 400 from the server.
func readDeclarations(arg string) (json.RawMessage, error) {
	raw := []byte(arg)
	if strings.HasPrefix(arg, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(arg, "@"))
		if err != nil {
			return nil, fmt.Errorf("read declarations: %w", err)
		}
		raw = b
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("declarations must be a JSON object of groups (purpose, datasets, environment, …): %w", err)
	}
	return json.RawMessage(raw), nil
}
