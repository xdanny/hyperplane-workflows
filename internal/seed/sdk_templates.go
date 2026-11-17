package seed

import (
	"encoding/json"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The same editable SDK definition is used by template launch and the node UI.
// No process registry, nested graph, or duplicate execution plan is involved.
func marketResearchTemplate() store.WorkflowTemplate {
	workflow := store.SDKWorkflow{Kind: "nodes", Instruction: `Research this market: {slot:market}.
Audience, buyer and budget: {slot:audience}.
Known players (a starting point, not a verified competitor set): {slot:competitorsHint}.
Handover format and recipient reference: {slot:handover}. Prepare the document for that handover; do not send or publish it. If unspecified, invent neither a recipient nor a format.`, Nodes: []sdkworkflow.Node{
		{ID: "sources", Kind: "research", Title: "Research five perspectives", Instruction: `Keep the search bounded: use at most three search/page-read rounds with batched queries, collect up to six useful sources, and report gaps instead of continuing to search. Find real problems for the declared market and audience using live sources. Cover all five perspectives: creative friction; everyday struggle; abandoned manual workarounds; problems from emerging behaviour; recurring underserved moments. These are five perspectives in one conversation, not independent researchers. Start from problems rather than apps. Use actual complaints from forums, reviews and communities. Keep quotes short and attach each source URL, community, date as displayed and community size as displayed; write "not shown" for missing metadata. Never estimate a date or community size. Record who is affected, what they do today, why it remains unsolved, and evidence limits. Do not present social feeds, marketplaces, generic to-do lists or habit trackers as novel opportunities.`},
		{ID: "research", Title: "Rank the evidenced problems", Instruction: `Using sources.md, write a standalone research brief. Aim for 6–8 distinct problems; require at least three with quoted source URLs. Do not pad a weak evidence base. Score every problem 1–5 on reach, evidence strength, emotional weight and unsolvedness; explain the observation behind every score, show the evidence count and rank by total score, explaining ties. Carry dates, community sizes and "not shown" verbatim. For each problem identify its audience, current workaround and evidence limitations. Lead with what was researched and the result in the first two sentences. Define jargon and abbreviations on first use; give units and denominators for numbers. End with the thinnest evidence and what the research cannot establish.`},
		{ID: "concepts", Title: "Propose buildable concepts", Instruction: `From research.md, propose three distinct, concrete concepts, each tied to one evidenced problem. Explain the user, rationale, expected impact, effort, first version and a deterministic offline-first core. An on-device capability must solve the stated problem rather than decorate it. Label concepts as unvalidated hypotheses. Preserve every evidence caveat; do not claim the proposed product has been built or market validated.`},
		{ID: "review", Kind: "check", Title: "Independently review the research and concepts", Instruction: `Read sources.md, research.md and concepts.md. Pass only if at least three distinct, concrete problems have short real quoted complaints with source URLs; all four scores, observations and evidence counts are shown and the ranking follows the scores; citation dates and community sizes match source evidence where shown, with missing metadata reported rather than fabricated; the brief reads on its own; and all three concepts are novel enough to distinguish from a thin clone, concretely buildable and have a deterministic offline-first core. Verify citations with live search. Inaccessible or undated evidence is a finding, but reject fabricated evidence and any problem whose evidence is too weak to justify its score. Check the opening verdict, definitions, units, denominators and preservation of caveats. Write a one-sentence restatement of what was attempted, what happened and what the reader should do next. Judge only these criteria and the declared market/audience; other opportunities or deeper sizing are findings, not invented requirements.`},
	}}
	spec, err := json.Marshal(workflow)
	if err != nil {
		panic(err)
	}
	return store.WorkflowTemplate{Name: "market-research", Description: "Research a market from five perspectives with live sources, rank the evidenced problems, propose three concepts, and have a separate model conversation review the work. Review and edit every step before running.", Spec: spec, ContextSlots: []store.ContextSlot{
		{ID: "market", Label: "Market", Hint: "The market or product space to research. Name it narrowly enough that the research can be useful.", Required: true, Example: "self-hosted time tracking for small agencies"},
		{ID: "audience", Label: "Audience", Hint: "Who buys and who uses, including the budget they control.", Required: true, Example: "owners of agencies with 5–30 people who bill from spreadsheets"},
		{ID: "competitorsHint", Label: "Players you already know", Hint: "Tools people already use. This is a starting point, not a verified competitor set.", Example: "Toggl, Harvest, Clockify"},
		handoverSlot("research write-up", "a markdown brief plus the ranked list, to #product-research"),
	}}
}
