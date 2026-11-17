package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdCompose proposes SDK steps through the API. JSON includes both
// the editable workflow and unsupported requests; it is never a launch command.
func cmdCompose(args []string, noColor bool) int {
	const use = "usage: hyperplane compose --brief \"<intent>\" [--project <name>] [--json]"
	brief := ""
	project := ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--brief" && i+1 < len(args):
			brief = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--brief="):
			brief = strings.TrimPrefix(args[i], "--brief=")
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--json":
			asJSON = true
		case !strings.HasPrefix(args[i], "--") && brief == "":
			brief = args[i]
		}
	}
	if strings.TrimSpace(brief) == "" {
		fmt.Fprintln(os.Stderr, use)
		return 2
	}

	cfg := config.Load()
	// Composition holds the connection across a codex call.
	cl := newEngineAPIClient(cfg.Addr)

	raw, status, err := cl.postJSON("/api/compose", map[string]string{"brief": brief, "project": project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "compose: %v\n", err)
		return 1
	}
	var out struct {
		Workflow    *store.SDKWorkflow `json:"workflow"`
		Unsupported []string           `json:"unsupported"`
		Error       string             `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Fprintf(os.Stderr, "compose: invalid server response: %v\n", err)
		return 1
	}
	if status != 200 || out.Error != "" {
		fmt.Fprintf(os.Stderr, "compose: %s\n", out.Error)
		return 1
	}
	if asJSON {
		os.Stdout.Write(raw)
		fmt.Println()
	} else {
		if out.Workflow != nil {
			fmt.Printf("%s\n", colorize(ansiBold, out.Workflow.Name, noColor))
			for i, node := range out.Workflow.Nodes {
				fmt.Printf("  %d. %s\n     %s\n", i+1, node.Title, node.Instruction)
				if node.Acceptance != "" {
					fmt.Printf("     Built-in check: %s\n", node.Acceptance)
				}
				keys := make([]string, 0, len(node.Inputs))
				for key := range node.Inputs {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					fmt.Printf("     %s: %s\n", key, node.Inputs[key])
				}
			}
		}
		for _, finding := range out.Unsupported {
			fmt.Printf("Not supported: %s\n", finding)
		}
		fmt.Println("Proposal only. Review the steps in Workflows before saving or running.")
	}
	if out.Workflow == nil || len(out.Unsupported) > 0 {
		return 1
	}
	return 0
}
