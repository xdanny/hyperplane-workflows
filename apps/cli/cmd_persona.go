// cmd_persona.go implements the `persona` verb: list the registered audit
// charters, and run one against a reviewable target.
//
// The charters live in personas/*.md and are amended in git. The CLI reads the
// registry and invokes a review; it never edits a charter, which is why there
// is no `persona edit`.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/persona"
)

// personaRow mirrors the API's persona view. It is declared here rather than
// shared so the CLI stays a plain HTTP client of its own server.
type personaRow struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Bar          string             `json:"bar"`
	Competence   persona.Competence `json:"competence"`
	Version      string             `json:"version"`
	Source       string             `json:"source"`
	Reviews      int                `json:"reviews"`
	CompetentFor string             `json:"competentFor"`
}

func cmdPersona(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		personaUsage()
		return 2
	}
	switch args[0] {
	case "list":
		return cmdPersonaList(args[1:], globalNoColor)
	case "review":
		return cmdPersonaReview(args[1:], globalNoColor)
	default:
		personaUsage()
		return 2
	}
}

func personaUsage() {
	fmt.Fprintln(os.Stderr, "usage: hyperplane persona <list|review>")
	fmt.Fprintln(os.Stderr, "  persona list [--competent-for skill:<id>] [--json]")
	fmt.Fprintln(os.Stderr, "  persona review <personaId> --target run:<id>|skill:<id> [--model m] [--json]")
}

// cmdPersonaList shows the roster: who is registered, what they claim
// competence over, and how many verdicts they have actually recorded.
func cmdPersonaList(args []string, globalNoColor bool) int {
	asJSON := false
	competentFor := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--competent-for":
			if i+1 < len(args) {
				competentFor = args[i+1]
				i++
			}
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)
	path := "/api/personas"
	if competentFor != "" {
		path += "?competentFor=" + competentFor
	}
	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "persona: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "persona: server returned %d: %s\n", status, body)
		return 1
	}
	if asJSON {
		printRawBody(body)
		return 0
	}

	var rows []personaRow
	if err := mustUnmarshal(body, &rows); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(rows) == 0 {
		if competentFor != "" {
			fmt.Printf("no registered persona claims competence over %s\n", competentFor)
			fmt.Println("competence is only ever claimed where a charter NAMED the skill — nothing is inferred.")
		} else {
			fmt.Println("no personas registered — run: make seed (or: hyperplane seed)")
		}
		return 0
	}

	noColor := globalNoColor || cl.noColor
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tREVIEWS\tCOMPETENCE")
	for _, p := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n",
			colorize(ansiBold, p.ID, noColor), p.Name, p.Reviews, competenceLabel(p.Competence))
	}
	_ = tw.Flush()
	for _, p := range rows {
		if p.Bar != "" {
			fmt.Printf("\n%s — bar: %q\n", p.ID, p.Bar)
		}
		if p.CompetentFor != "" {
			fmt.Printf("  competent because they %s\n", p.CompetentFor)
		}
	}
	fmt.Println("\nA persona verdict is advisory evidence. It never gates an approval; the human decides.")
	return 0
}

// competenceLabel renders a persona's claimed competence in one line.
func competenceLabel(c persona.Competence) string {
	var parts []string
	if len(c.Skills) > 0 {
		parts = append(parts, "skills: "+strings.Join(c.Skills, ", "))
	}
	if len(c.Processes) > 0 {
		parts = append(parts, "processes: "+strings.Join(c.Processes, ", "))
	}
	if c.Wording {
		parts = append(parts, "the wording of any instruction")
	}
	if len(parts) == 0 {
		if len(c.Domains) > 0 {
			// Domains are free text and never routed on — say so rather than
			// letting a reader take them for a machine-checkable claim.
			return "(free-text domains only — not routable)"
		}
		return "-"
	}
	return strings.Join(parts, "; ")
}

// cmdPersonaReview runs one persona against one target.
// Usage: hyperplane persona review <id> --target run:<id>|skill:<id> [--model m] [--json]
func cmdPersonaReview(args []string, globalNoColor bool) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		personaUsage()
		return 2
	}
	id := args[0]
	target, model := "", ""
	asJSON := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--target":
			if i+1 < len(args) {
				target = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				model = args[i+1]
				i++
			}
		}
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "persona review: --target run:<runId> or --target skill:<skillId> is required")
		return 2
	}
	if _, err := persona.ParseTarget(target); err != nil {
		fmt.Fprintf(os.Stderr, "persona review: %v\n", err)
		return 2
	}

	cfg := config.Load()
	// A review holds the connection across one codex call, like compose and
	// skills extract — the default 10s client would hang up mid-review.
	cl := newEngineAPIClient(cfg.Addr)
	if !asJSON {
		// A review is a billed engine call that reads a lot of evidence. Say what
		// is happening rather than leaving a silent terminal for minutes.
		fmt.Printf("running %s against %s — one read-only engine call, this takes a while…\n", id, target)
	}
	body, status, err := cl.postJSON("/api/personas/"+id+"/review",
		map[string]string{"target": target, "model": model})
	if err != nil {
		fmt.Fprintf(os.Stderr, "persona review: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "persona review: server returned %d: %s\n", status, body)
		return 1
	}
	if asJSON {
		printRawBody(body)
		return 0
	}

	var rev persona.Review
	if err := mustUnmarshal(body, &rev); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	noColor := globalNoColor || cl.noColor
	fmt.Printf("\n%s — %s\n", colorize(ansiBold, rev.PersonaName, noColor), rev.Target)
	fmt.Printf("verdict: %s\n", colorize(verdictColor(rev.Verdict), strings.ToUpper(rev.Verdict), noColor))
	if rev.Summary != "" {
		fmt.Printf("\n%s\n", rev.Summary)
	}
	for _, reason := range rev.Reasons {
		fmt.Printf("\n  - %s\n", reason)
	}
	for _, c := range rev.Citations {
		fmt.Printf("\n  %s\n    %q\n", colorize(ansiDim, c.Ref, noColor), c.Quote)
	}
	if rev.RevisedWording != "" {
		fmt.Printf("\n%s\n%s\n", colorize(ansiBold, "Proposed replacement wording:", noColor), rev.RevisedWording)
	}
	fmt.Printf("\nrecorded as %s — advisory evidence: it gates nothing, and you decide.\n", rev.ID)
	return 0
}

// printRawBody echoes a JSON response body verbatim for --json consumers.
func printRawBody(body []byte) {
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		printJSON(raw)
		return
	}
	fmt.Println(string(body))
}

func verdictColor(v string) string {
	switch v {
	case persona.VerdictApprove, persona.VerdictPass:
		return ansiGreen
	case persona.VerdictReject, persona.VerdictFail:
		return ansiRed
	default:
		return ansiYellow
	}
}
