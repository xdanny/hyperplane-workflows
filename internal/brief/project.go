package brief

// project.go — "what this project has built so far".
//
// This writes `projects.narrative`, and NOTHING here ever writes
// `projects.summary`. The two are different sentences with different authors:
// `summary` is the owner's "what this project is for", and `narrative` is the
// platform's account of what the project actually delivered. They shared one
// column briefly, and the result was that accepting a delivery overwrote the
// owner's description, while a project with zero runs rendered the owner's
// typed intent under a heading claiming it was a result. Migration 0026 split
// them; the regression test for that is TestWriteProjectNeverTouchesSummary.
//
// The narrative is written not from the project's plan, which is a statement of
// intent, but from the briefs of the deliveries it actually got accepted. A
// project narrative is the accumulation of true outcomes.
//
// It also carries the lines that closed WITHOUT an acceptance — labelled as
// exactly that. They are not what the project built; leaving them out made the
// account of "what this project has built so far" silently omit most of the
// project's runs.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ErrNoAcceptedDeliveries is returned by WriteProject when the project has no
// accepted delivery with a brief to write a narrative from.
//
// It exists so that nothing is written in that case. An empty `narrative` is
// already the honest statement of the absence — the project page renders
// "Nothing has been accepted under this project yet." from it — and writing
// that sentence into the column would be storing a fact the emptiness already
// carries. The automatic path treats it as the no-op it is; the explicit
// endpoint reports it.
var ErrNoAcceptedDeliveries = errors.New("brief: this project has no accepted delivery with a brief to write a narrative from")

// maxProjectSources bounds how many accepted deliveries the narrative is
// written from. Ten is already more history than a paragraph can carry, and the
// newest ones are what "so far" means.
const maxProjectSources = 10

// maxProjectNarrative caps what is written to projects.narrative. It is a
// paragraph on a card, not a document.
const maxProjectNarrative = 2000

// CollectProject assembles the state document for a project narrative: the
// briefs of its accepted deliveries, newest first.
//
// Only ACCEPTED deliveries contribute. A rejected delivery is a real outcome
// and keeps its own brief, but it is not something the project built, and a
// narrative that swept it in would describe work the project does not have.
func CollectProject(ctx context.Context, st *store.Store, project string) (StateDoc, error) {
	doc := StateDoc{
		Kind:      store.BriefSubjectProject,
		SubjectID: project,
		Project:   project,
	}
	rows, err := st.ListBriefs(ctx, project, store.BriefSubjectDelivery, maxProjectSources*3)
	if err != nil {
		return doc, fmt.Errorf("brief: list project briefs: %w", err)
	}
	for _, row := range rows {
		if len(doc.Sources) >= maxProjectSources {
			doc.Notes = append(doc.Notes,
				fmt.Sprintf("Only the %d most recent accepted deliveries are included; this project has more.", maxProjectSources))
			break
		}
		// Either grain: a delivery accepted through the inbox carries a
		// journey-grain decision, and checking only run grain would leave the
		// narrative empty for exactly the deliveries people actually accept.
		verdict := ""
		if d, derr := st.DeliveryDecisionForRun(ctx, row.SubjectID); derr == nil {
			verdict = d.Decision
		}
		if verdict != "accepted" {
			continue
		}
		doc.Sources = append(doc.Sources, Source{
			SubjectID: row.SubjectID,
			Verdict:   verdict,
			When:      row.CreatedAt,
			Body:      ParseDoc(row.Body).Text(),
			Grounded:  row.Grounded,
		})
	}
	doc.ClosedLines = collectClosedLines(ctx, st, project, doc.Sources)
	doc.Notes = append(doc.Notes, declarationCaveats(ctx, st, project)...)
	return doc, nil
}

// declarationCaveats carries what is WEAK about the project's declarations into
// the state document — today, a holdout declared without a content hash.
//
// A narrative written over runs scored on "the June holdout" reads as if the
// bytes were pinned. They are not: without a sha256 nothing can prove which
// rows produced any of those numbers, and the absence has to travel with the
// account rather than be silently omitted from it.
//
// Best-effort: a project whose declarations cannot be read still gets its
// narrative, without a caveat it cannot substantiate.
func declarationCaveats(ctx context.Context, st *store.Store, project string) []string {
	p, err := st.GetProject(ctx, project)
	if err != nil {
		return nil
	}
	decl, err := store.ParseDeclarations(p.Declarations)
	if err != nil {
		return nil
	}
	return store.DeclarationCaveats(decl)
}

// collectClosedLines gathers the project's delivery lines that ended without an
// acceptance — Q-17's first half.
//
// The narrative used to be written from accepted deliveries alone, which made
// "what this project has built so far" omit a line that took five of the
// project's seven runs and left a fixed contract behind. These lines belong in
// the state document, clearly labelled as what they are, so the narrative can
// say what remains from them instead of not knowing they happened.
//
// Best-effort throughout: a project narrative that loses a closed line is worse
// than one without it, but neither is worth failing the write over.
func collectClosedLines(ctx context.Context, st *store.Store, project string, accepted []Source) []ClosedLine {
	acceptedRuns := make(map[string]bool, len(accepted))
	for _, s := range accepted {
		acceptedRuns[s.SubjectID] = true
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{
		Project: store.StrPtr(project), TopLevel: true, Limit: store.JourneyRunLimit,
	})
	if err != nil || len(runs) == 0 {
		return nil
	}

	// One resolver for the whole project, so a closed line is called what the
	// Inbox calls it rather than by the template's uuid.
	namer, _ := st.NewRecipeNamer(ctx)

	var out []ClosedLine
	for _, b := range journey.Group(runs) {
		b.RecipeName = namer.Name(b.Recipe)
		if len(b.Runs) == 0 || len(out) >= maxProjectSources {
			continue
		}
		// Group sorts a bucket's runs newest-first, so this is the line's
		// current attempt — the one that speaks for it.
		latest := b.Runs[0]
		if acceptedRuns[latest.RunID] || !journey.Terminal(latest.State) {
			continue
		}
		line := ClosedLine{
			Name:     lineName(latest, b),
			RunID:    latest.RunID,
			Attempts: len(b.Runs),
			State:    latest.State,
		}
		if d, derr := st.DeliveryDecisionForRun(ctx, latest.RunID); derr == nil {
			if d.Decision == "accepted" {
				// Accepted with no brief to quote: it is not a closed line, and
				// claiming it as one would invert the project's own record.
				continue
			}
			line.Verdict = humanVerdict(d.Decision)
		}
		// The line's own account of itself, when the platform wrote one. Either
		// grain: the delivery brief if it exists, else the run brief.
		for _, kind := range []string{store.BriefSubjectDelivery, store.BriefSubjectRun} {
			row, berr := st.LatestBrief(ctx, kind, latest.RunID)
			if berr != nil {
				continue
			}
			if reason := strings.TrimSpace(ParseDoc(row.Body).TrueOutcome); reason != "" {
				line.Reason = reason
				break
			}
		}
		out = append(out, line)
	}
	return out
}

// lineName is what to call a delivery line: its newest attempt's goal, else the
// recipe it came from, else its DAG. Same order the journey card uses, so a
// line is called the same thing in the narrative as on screen.
func lineName(latest store.Run, b journey.Bucket) string {
	if goal := strings.TrimSpace(latest.Goal); goal != "" {
		return goal
	}
	// The recipe's name, never its uuid.
	if b.RecipeName != "" {
		return b.RecipeName
	}
	if b.Recipe != "" {
		return b.Recipe
	}
	return b.DAG
}

// WriteProject regenerates a project's narrative and writes it to
// projects.narrative — never to projects.summary, which belongs to the person
// who started the project.
//
// The brief row is recorded first and the narrative column is a projection of it,
// so the narrative is never a value with no provenance: whoever reads "this
// project has built…" on a card can find the exact brief it came from, its
// engine, and whether its numbers were verified.
func WriteProject(ctx context.Context, st *store.Store, cfg Config, project string) (store.Brief, error) {
	if strings.TrimSpace(project) == "" {
		return store.Brief{}, fmt.Errorf("brief: project narrative needs a project")
	}
	doc, err := CollectProject(ctx, st, project)
	if err != nil {
		return store.Brief{}, err
	}
	if len(doc.Sources) == 0 {
		return store.Brief{}, ErrNoAcceptedDeliveries
	}
	row, err := Write(ctx, st, cfg, doc)
	if err != nil {
		return store.Brief{}, err
	}

	narrative := ProjectNarrative(ParseDoc(row.Body), row)
	if narrative == "" {
		return row, nil
	}
	// A project tagged on runs may never have been registered as a row (a
	// database written before projects had rows). Register it rather than
	// dropping the narrative on the floor — EnsureProject is a no-op otherwise.
	if err := st.EnsureProject(ctx, project); err != nil {
		return row, fmt.Errorf("brief: register project %q: %w", project, err)
	}
	if _, err := st.UpdateProject(ctx, project, store.ProjectPatch{Narrative: &narrative}); err != nil {
		// The narrative is recorded either way; failing to project it onto the
		// card is worth reporting, not worth losing the brief over.
		return row, fmt.Errorf("brief: write project narrative: %w", err)
	}
	return row, nil
}

// ProjectNarrative renders "what this project has built so far" for the project
// card.
//
// It carries the honesty flags with it. A narrative whose numbers were never
// verified says so in the text, because a project card has no room for a badge
// and a bare paragraph reads as established fact.
func ProjectNarrative(d Doc, row store.Brief) string {
	parts := []string{strings.TrimSpace(d.WhatItBuilds), strings.TrimSpace(d.TrueOutcome)}
	out := make([]string, 0, 3)
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ""
	}
	if !row.Grounded {
		out = append(out, "(Numbers in this account could not be verified against the platform's own records.)")
	}
	s := strings.Join(out, " ")
	if len(s) > maxProjectNarrative {
		s = collapse(s, maxProjectNarrative)
	}
	return s
}
