// Command hyperplane is the CLI. It provides rich TTY output for humans and
// --json on every command for orchestrator (Claude Code) consumption.
//
// SDK workflows, project tasks, run inspection, approvals and knowledge tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/envfile"
	"github.com/xdanny/hyperplane-workflows/internal/persona"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const version = "0.0.0-m0"

func main() {
	envfile.Load(".env") // pick up gitignored local secrets (e.g. CONTEXT7_API_KEY)
	// Strip global --no-color flag before dispatching.
	args := os.Args[1:]
	noColor := false
	filtered := args[:0]
	for _, a := range args {
		if a == "--no-color" {
			noColor = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	verb := args[0]
	rest := args[1:]

	switch verb {
	case "version":
		fmt.Println("hyperplane", version)
	case "doctor":
		os.Exit(doctor(rest, noColor))
	case "task":
		os.Exit(cmdTask(rest))
	case "ideator":
		os.Exit(cmdIdeator(rest))
	case "seed":
		os.Exit(seedKnowledge(rest))
	case "runs":
		os.Exit(cmdRuns(rest, noColor))
	case "show":
		os.Exit(cmdShow(rest, noColor))
	case "watch":
		os.Exit(cmdWatch(rest, noColor))
	case "skills":
		os.Exit(cmdSkills(rest, noColor))
	case "inbox":
		os.Exit(cmdInbox(rest, noColor))
	case "approve":
		os.Exit(cmdApprove(rest, noColor))
	case "select":
		os.Exit(cmdSelect(rest, noColor))
	case "cancel":
		os.Exit(cmdCancel(rest, noColor))
	case "compose":
		os.Exit(cmdCompose(rest, noColor))
	case "eval":
		ctx := context.Background()
		os.Exit(cmdEval(ctx, rest))
	case "optimize":
		ctx := context.Background()
		os.Exit(cmdOptimize(ctx, rest))
	case "orchestrate":
		ctx := context.Background()
		os.Exit(cmdOrchestrate(ctx, rest, noColor))
	case "workflow":
		os.Exit(cmdWorkflow(rest, noColor))
	case "template":
		os.Exit(cmdTemplate(rest, noColor))
	case "route":
		ctx := context.Background()
		os.Exit(cmdRoute(ctx, rest, noColor))
	case "projects":
		os.Exit(cmdProjects(rest, noColor))
	case "journeys":
		os.Exit(cmdJourneys(rest, noColor))
	case "lane":
		os.Exit(cmdLane(rest, noColor))
	case "ticket":
		os.Exit(cmdTicket(rest, noColor))
	case "workers":
		os.Exit(cmdWorkers(rest, noColor))
	case "improve":
		ctx := context.Background()
		os.Exit(cmdImprove(ctx, rest, noColor))
	case "tech-radar":
		os.Exit(cmdTechRadar(rest, noColor))
	case "kb":
		os.Exit(cmdKB(rest, noColor))
	case "knowledge":
		os.Exit(cmdKnowledge(rest, noColor))
	case "persona":
		os.Exit(cmdPersona(rest, noColor))
	case "--help", "-h", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", verb)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hyperplane <verb> [args] [--json] [--no-color]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Local SDK and setup:")
	fmt.Fprintln(os.Stderr, "  version                          print version")
	fmt.Fprintln(os.Stderr, "  doctor                           check API health")
	fmt.Fprintln(os.Stderr, "  task --target DIR [--check JSON] GOAL  run an SDK task")
	fmt.Fprintln(os.Stderr, "  ideator [--auto-repair] BRIEF    create app ideas through the SDK")
	fmt.Fprintln(os.Stderr, "  seed [--from <dir>]              import skills+context (default: knowledge/defs)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "API verbs (require server: go run ./apps/api):")
	fmt.Fprintln(os.Stderr, "  runs [--state X] [--limit N]     list recent runs")
	fmt.Fprintln(os.Stderr, "  runs outcome <runId> --verdict V annotate ONE attempt (deliveries decide once:")
	fmt.Fprintln(os.Stderr, "                                   see `journeys outcome`)")
	fmt.Fprintln(os.Stderr, "  runs log <runId> <nodeId>        print a node's log ([--tail N] for the last N lines)")
	fmt.Fprintln(os.Stderr, "  show <runId>                     show run detail + node table")
	fmt.Fprintln(os.Stderr, "  watch <runId>                    stream events until terminal state")
	fmt.Fprintln(os.Stderr, "  skills [query]                   search the skill knowledge base")
	fmt.Fprintln(os.Stderr, "  skills extract --run <runId>     distil a reusable skill from a passed run (draft)")
	fmt.Fprintln(os.Stderr, "  skills approve <skillId>         approve a draft skill so nodes can use it")
	fmt.Fprintln(os.Stderr, "  skills reject <skillId>          discard a draft skill")
	fmt.Fprintln(os.Stderr, "  persona list                     the registered audit charters + what they judge")
	fmt.Fprintln(os.Stderr, "       [--competent-for skill:<id>]")
	fmt.Fprintln(os.Stderr, "  persona review <id>              run a persona against a run or skill (advisory)")
	fmt.Fprintln(os.Stderr, "       --target run:<id>|skill:<id> [--model m]")
	fmt.Fprintln(os.Stderr, "  inbox                            show pending approvals + selections")
	fmt.Fprintln(os.Stderr, "  approve <id> [--reject]          approve or reject a gate")
	fmt.Fprintln(os.Stderr, "  select <id> <winner>             resolve a selection gate")
	fmt.Fprintln(os.Stderr, "  cancel <runId>                   cancel a running run (marks it interrupted)")
	fmt.Fprintln(os.Stderr, "  compose --brief \"<intent>\" [--project <p>] [--json]  turn an intent into a workflow plan")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Saved workflows (the thing you keep and run again):")
	fmt.Fprintln(os.Stderr, "  workflow list [--project p]      list saved workflows with their step and input counts")
	fmt.Fprintln(os.Stderr, "  workflow show <id|name>          inputs (with hints and examples), steps and recent runs")
	fmt.Fprintln(os.Stderr, "  workflow run <id|name>           run it again with this run's values")
	fmt.Fprintln(os.Stderr, "           [--input k=v ...]       one per declared input (repeatable)")
	fmt.Fprintln(os.Stderr, "           [--project p] [--watch] [--json]")
	fmt.Fprintln(os.Stderr, "  workflow runs <id|name>          its run history: inputs, duration, checks, decision")
	fmt.Fprintln(os.Stderr, "           [--limit N] [--json]")
	fmt.Fprintln(os.Stderr, "                                   Hyperplane does not schedule runs; put `workflow run`")
	fmt.Fprintln(os.Stderr, "                                   in cron or launchd to repeat one on a timer.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Recipes (templates that declare what must be known + proven):")
	fmt.Fprintln(os.Stderr, "  template list                    list templates; recipes are marked")
	fmt.Fprintln(os.Stderr, "  template show <id|name>          print its context slots + criteria")
	fmt.Fprintln(os.Stderr, "  template instantiate <id|name>   fill inputs and prepare an SDK workflow")
	fmt.Fprintln(os.Stderr, "           [--slot k=v ...]        one per context slot (repeatable)")
	fmt.Fprintln(os.Stderr, "           [--run]               also start the reviewed workflow")
	fmt.Fprintln(os.Stderr, "           [--project <name>] [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Evals (M11):")
	fmt.Fprintln(os.Stderr, "  eval list                        list evalsets")
	fmt.Fprintln(os.Stderr, "  eval run <id> [--mock] [--json]  run an evalset")
	fmt.Fprintln(os.Stderr, "  eval show <runId>                show eval run detail")
	fmt.Fprintln(os.Stderr, "  eval compare <runA> <runB>       compare two eval runs")
	fmt.Fprintln(os.Stderr, "  eval seed [--from <dir>]         seed evalsets from *.eval.json files")
	fmt.Fprintln(os.Stderr, "  eval gen <skillId>               generate + store a harsh evalset for one skill")
	fmt.Fprintln(os.Stderr, "           [--engine codex|local]  model to use (default local)")
	fmt.Fprintln(os.Stderr, "           [--json]                emit generated evalset as JSON")
	fmt.Fprintln(os.Stderr, "  eval gen --all                   generate evalsets for all skills")
	fmt.Fprintln(os.Stderr, "           [--only-missing]        skip skills that already have an evalset")
	fmt.Fprintln(os.Stderr, "           [--max N]               cap number of skills to process")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Backlog Orchestrator (M21):")
	fmt.Fprintln(os.Stderr, "  orchestrate --backlog <file.json>   run checked SDK tasks from a backlog")
	fmt.Fprintln(os.Stderr, "              [--max N] [--engine codex-sdk] [--target <dir>]")
	fmt.Fprintln(os.Stderr, "              [--dry-run] [--stop-on-fail] [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Optimize (M11/M18):")
	fmt.Fprintln(os.Stderr, "  optimize <skillId>               propose+score variants, keep best if it beats baseline")
	fmt.Fprintln(os.Stderr, "           [--evalset <id>]        override auto-detected evalset")
	fmt.Fprintln(os.Stderr, "           [--variants N]          number of variants to propose (default 4)")
	fmt.Fprintln(os.Stderr, "           [--proposer local|codex] model used to generate variants (default local)")
	fmt.Fprintln(os.Stderr, "           [--score-engine local|codex] model used to run eval cases (default local)")
	fmt.Fprintln(os.Stderr, "           [--min-gain <f>]        minimum passRate gain to accept winner (default 0)")
	fmt.Fprintln(os.Stderr, "           [--apply]               persist winning instruction to skill store")
	fmt.Fprintln(os.Stderr, "  optimize list <skillId>          list past optimize runs")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Always-on self-improvement (M26):")
	fmt.Fprintln(os.Stderr, "  improve [--once]                 run one self-improvement pass (--once is the default/only mode)")
	fmt.Fprintln(os.Stderr, "          [--skill <id>]           limit to a specific skill (repeatable)")
	fmt.Fprintln(os.Stderr, "          [--max N]                cap number of skills processed")
	fmt.Fprintln(os.Stderr, "          [--min-feedback K]       minimum feedback items required (default 3)")
	fmt.Fprintln(os.Stderr, "          [--apply]               persist winning instruction to skill store")
	fmt.Fprintln(os.Stderr, "          [--engine e]             score engine (local|codex, default local)")
	fmt.Fprintln(os.Stderr, "          [--dry-run]              skip optimize; mark all skills skipped")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Governed local routing (M25):")
	fmt.Fprintln(os.Stderr, "  route eval --skill <id> --evalset <id> [--margin 0.1]  eval-driven engine selection")
	fmt.Fprintln(os.Stderr, "  route list                                               list routing policies")
	fmt.Fprintln(os.Stderr, "  route set --scope <s> --engine <e>                      manual override")
	fmt.Fprintln(os.Stderr, "  route clear --scope <s>                                 remove a policy")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Projects (the scope runs, lanes and decisions are tagged with):")
	fmt.Fprintln(os.Stderr, "  projects [list] [--include-test]  list projects with per-kind counts")
	fmt.Fprintln(os.Stderr, "  projects create --id <id>         register a project")
	fmt.Fprintln(os.Stderr, "           [--name <n>] [--summary <text>] [--owner <who>]")
	fmt.Fprintln(os.Stderr, "  projects show <projectId>         counts + the workflows it has run")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Deliveries (journeys — one product's story across its runs, derived):")
	fmt.Fprintln(os.Stderr, "  journeys [list] [--project <id>]  deliveries, newest activity first")
	fmt.Fprintln(os.Stderr, "           [--needs-decision]       only the ones waiting on your verdict")
	fmt.Fprintln(os.Stderr, "  journeys show <key>               chapters + what happened, in order")
	fmt.Fprintln(os.Stderr, "  journeys outcome <key> --verdict V  decide the whole delivery, once")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Board tasks (managed SDK):")
	fmt.Fprintln(os.Stderr, "  lane create --name <n> [--desc <d>] [--target <path>]  create a swim-lane")
	fmt.Fprintln(os.Stderr, "  lane list                                               list lanes with ticket counts")
	fmt.Fprintln(os.Stderr, "  ticket add --lane <id> --title <t> [--detail <d>]      add a ticket to a lane")
	fmt.Fprintln(os.Stderr, "             [--priority N] [--deps id,id]")
	fmt.Fprintln(os.Stderr, "  ticket list --lane <id>                                 list tickets in a lane")
	fmt.Fprintln(os.Stderr, "  workers --lane <id> [--json]                          run next ready task for human review")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Knowledge layer:")
	fmt.Fprintln(os.Stderr, "  tech-radar list [--json]                           list tech radar entries grouped by quadrant/ring")
	fmt.Fprintln(os.Stderr, "  kb search <query> [--kinds k,k] [--limit N] [--json]  unified search: skills+memory+radar+decisions")
	fmt.Fprintln(os.Stderr, "  kb memory list [--kind <k>] [--json]               list memory items")
	fmt.Fprintln(os.Stderr, "  kb memory set <key> <json>                         upsert a memory item")
	fmt.Fprintln(os.Stderr, "  kb memory delete <key>                             remove a memory item")
	fmt.Fprintln(os.Stderr, "  knowledge save <runId> [--target <dir>] [--json]   export saved SDK receipt into knowledge/ dir")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Global flags:")
	fmt.Fprintln(os.Stderr, "  --json      emit machine-parseable JSON (all verbs)")
	fmt.Fprintln(os.Stderr, "  --no-color  disable ANSI color (also: set NO_COLOR env)")
}

// seedKnowledge imports a knowledge base (skills, context bundles) into Postgres.
func seedKnowledge(args []string) int {
	from := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			from = args[i+1]
			i++
		}
	}
	cfg := config.Load()
	// Default to the in-repo starter defs so a fresh clone can seed with no
	// arguments and no external checkout. --from still points anywhere.
	if from == "" {
		from = filepath.Join(cfg.PlatformRoot, "knowledge", "defs")
	}
	// FromDefsDir treats a missing skills/ or context/ dir as "nothing to import",
	// so a typo in --from would otherwise report a cheerful "seeded 0 skills".
	if fi, err := os.Stat(from); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "seed: defs dir not found: %s\n", from)
		return 2
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return 1
	}
	res, err := seed.FromDefsDir(ctx, st, from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		return 1
	}
	// Refresh persona content without changing operator-managed status.
	people, plerr := persona.Import(ctx, st, filepath.Join(cfg.PlatformRoot, "personas"))
	if plerr != nil {
		fmt.Fprintf(os.Stderr, "seed personas: %v\n", plerr)
		return 1
	}
	// Seed platform defaults: tech radar (with skill links), stacks, categories, templates.
	if derr := seed.Defaults(ctx, st); derr != nil {
		fmt.Fprintf(os.Stderr, "seed defaults: %v\n", derr)
		return 1
	}
	fmt.Printf("seeded %d skills, %d context bundles, %d personas\n",
		res.Skills, res.ContextBundles, len(people))
	return 0
}

// doctor pings the running API's health endpoint.
// Supports --json for machine output.
func doctor(args []string, globalNoColor bool) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	cfg := config.Load()
	url := "http://" + cfg.Addr + "/api/health"
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)

	type healthOut struct {
		OK          bool   `json:"ok"`
		DB          string `json:"db"`
		Codex       string `json:"codex"`
		LocalEngine string `json:"localEngine,omitempty"`
		Time        string `json:"time,omitempty"`
		Detail      string `json:"detail,omitempty"`
		Error       string `json:"error,omitempty"`
	}

	if err != nil {
		if asJSON {
			printJSON(healthOut{OK: false, Error: err.Error()})
		} else {
			fmt.Printf("✗ api unreachable at %s: %v\n", url, err)
		}
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var h healthOut
	_ = json.Unmarshal(body, &h)

	if asJSON {
		printJSON(h)
		if h.OK {
			return 0
		}
		return 1
	}

	noColor := globalNoColor
	_, noColorEnv := os.LookupEnv("NO_COLOR")
	if noColorEnv {
		noColor = true
	}

	fmt.Printf("api          %s\n", url)
	fmt.Printf("db           %s\n", h.DB)
	fmt.Printf("codex        %s\n", h.Codex)
	if h.LocalEngine != "" {
		fmt.Printf("local engine %s\n", h.LocalEngine)
	}
	if h.OK {
		fmt.Printf("%s healthy\n", colorize(ansiGreen, "✓", noColor))
		return 0
	}
	fmt.Printf("%s degraded\n", colorize(ansiRed, "✗", noColor))
	if h.Detail != "" {
		fmt.Printf("detail       %s\n", h.Detail)
	}
	return 1
}
