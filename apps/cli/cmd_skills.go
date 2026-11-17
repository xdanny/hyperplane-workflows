// cmd_skills.go implements the `skills` verb: searches and lists skills from the
// API, and drives the extraction lifecycle (extract → approve/reject).
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdSkills dispatches the `skills` verb. Bare arguments are a search query,
// so the sub-commands are matched first.
func cmdSkills(args []string, globalNoColor bool) int {
	if len(args) > 0 {
		switch args[0] {
		case "extract":
			return cmdSkillsExtract(args[1:], globalNoColor)
		case "approve":
			return cmdSkillsDecide(args[1:], "approve", globalNoColor)
		case "reject":
			return cmdSkillsDecide(args[1:], "reject", globalNoColor)
		}
	}
	return cmdSkillsList(args, globalNoColor)
}

// cmdSkillsList fetches and displays skills from the running API server.
// Usage: hyperplane skills [query] [--json]
func cmdSkillsList(args []string, globalNoColor bool) int {
	query := ""
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if len(a) > 0 && a[0] != '-' {
			query = a
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := "/api/skills"
	if query != "" {
		path += "?q=" + query
	}

	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "skills: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var skills []store.Skill
	if err := mustUnmarshal(body, &skills); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor
	if len(skills) == 0 {
		if query != "" {
			fmt.Printf("no skills matching %q\n", query)
		} else {
			fmt.Println("no skills found — run: make seed (or: hyperplane seed)")
		}
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tCATEGORY\tDESCRIPTION")
	drafts := 0
	for _, sk := range skills {
		desc := sk.Description
		if len(desc) > 70 {
			desc = desc[:67] + "..."
		}
		cat := sk.Category
		if cat == "" {
			cat = "-"
		}
		status := store.SkillStatus(sk)
		if store.IsDraftSkill(sk) {
			drafts++
			status = colorize(ansiYellow, status, noColor)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			colorize(ansiBold, sk.ID, noColor), status, cat, desc)
	}
	_ = tw.Flush()
	if drafts > 0 {
		fmt.Printf("\n%d draft(s) awaiting approval — drafts are never used until approved: hyperplane skills approve <id>\n", drafts)
	}
	return 0
}

// cmdSkillsExtract asks the server to distil a reusable skill out of a passed
// run and store it as a draft.
// Usage: hyperplane skills extract --run <runId> [--json]
func cmdSkillsExtract(args []string, globalNoColor bool) int {
	runID := ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--run" && i+1 < len(args):
			runID = args[i+1]
			i++
		case args[i] == "--json":
			asJSON = true
		case len(args[i]) > 0 && args[i][0] != '-' && runID == "":
			runID = args[i]
		}
	}
	if runID == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane skills extract --run <runId> [--json]")
		return 2
	}

	cfg := config.Load()
	// Extraction holds the connection across a codex call.
	cl := newEngineAPIClient(cfg.Addr)
	raw, status, err := cl.postJSON("/api/runs/"+runID+"/extract-skill", struct{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills extract: %v\n", err)
		return 1
	}

	if asJSON {
		os.Stdout.Write(raw)
		fmt.Println()
		if status != http.StatusOK {
			return 1
		}
		return 0
	}

	if status != http.StatusOK {
		var errOut struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errOut)
		if errOut.Error == "" {
			errOut.Error = string(raw)
		}
		fmt.Fprintf(os.Stderr, "skills extract: %s\n", errOut.Error)
		return 1
	}

	var res knowledge.ExtractResult
	if err := mustUnmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		noColor = true
	}

	switch res.Status {
	case knowledge.ResultDuplicate:
		if res.Duplicate != nil {
			fmt.Printf("%s  %s (%.0f%% similar)\n",
				colorize(ansiYellow, "duplicate", noColor),
				colorize(ansiBold, res.Duplicate.SkillID, noColor),
				res.Duplicate.Similarity*100)
			fmt.Printf("  %s\n", res.Duplicate.Advice)
		}
		// A near-duplicate of a DRAFT is not a dead end any more: this run has
		// joined that draft's support, so say what it now rests on.
		if res.SupportRuns > 0 {
			fmt.Printf("  support     %d run(s)%s\n", res.SupportRuns, anecdoteNote(res.SupportRuns))
		}
	case knowledge.ResultDrafted:
		if res.Skill != nil {
			fmt.Printf("%s  %s — %s\n",
				colorize(ansiGreen, "draft", noColor),
				colorize(ansiBold, res.Skill.ID, noColor),
				res.Skill.Name)
			if res.Skill.Category != "" {
				fmt.Printf("  domain      %s\n", res.Skill.Category)
			}
		}
		// Support and evidence, not a bare confidence scalar: "0.90" explains
		// nothing, while "one run" and two quotes a reader can open say exactly
		// how much is behind this proposal.
		if res.SupportRuns > 0 {
			fmt.Printf("  support     %d run(s)%s\n", res.SupportRuns, anecdoteNote(res.SupportRuns))
		}
		if res.Proposal != nil {
			if res.Proposal.Rationale != "" {
				fmt.Printf("  rationale   %s\n", res.Proposal.Rationale)
			}
			for _, ev := range res.Proposal.Evidence {
				fmt.Printf("  evidence    %s: %q\n", ev.NodeID, ev.Quote)
			}
		}
	}
	if res.Message != "" {
		fmt.Printf("\n%s\n", res.Message)
	}
	return 0
}

// anecdoteNote refuses to let a single run read as a track record.
func anecdoteNote(support int) string {
	if support <= 1 {
		return " — a single run, so treat this as an anecdote until another run supports it"
	}
	return ""
}

// cmdSkillsDecide approves or rejects a draft skill.
// Usage: hyperplane skills approve|reject <skillId> [--json]
func cmdSkillsDecide(args []string, action string, globalNoColor bool) int {
	skillID := ""
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if len(a) > 0 && a[0] != '-' && skillID == "" {
			skillID = a
		}
	}
	if skillID == "" {
		fmt.Fprintf(os.Stderr, "usage: hyperplane skills %s <skillId> [--json]\n", action)
		return 2
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	body, status, err := cl.postJSON("/api/skills/"+skillID+"/"+action, map[string]any{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills %s: %v\n", action, err)
		return 1
	}
	if asJSON {
		os.Stdout.Write(body)
		fmt.Println()
		if status != http.StatusOK {
			return 1
		}
		return 0
	}
	if status != http.StatusOK {
		var errOut struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &errOut)
		if errOut.Error == "" {
			errOut.Error = string(body)
		}
		fmt.Fprintf(os.Stderr, "skills %s: %s\n", action, errOut.Error)
		return 1
	}

	noColor := globalNoColor
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		noColor = true
	}
	if action == "reject" {
		fmt.Printf("%s  draft %s discarded\n",
			colorize(ansiYellow, "rejected", noColor), colorize(ansiBold, skillID, noColor))
		return 0
	}
	var sk store.Skill
	_ = json.Unmarshal(body, &sk)
	name := sk.Name
	if name == "" {
		name = skillID
	}
	fmt.Printf("%s  %s — %s is now usable by workflow nodes\n",
		colorize(ansiGreen, "approved", noColor), colorize(ansiBold, skillID, noColor), name)
	return 0
}
