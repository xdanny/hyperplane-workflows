package seed

import (
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Recurring templates are the five workflows the round-11 personas said they
// would rerun every week or month. Their shape is deliberate and identical:
// the facts that CHANGE between periods are context slots, the judgement that
// must not change is an independent check whose failure criteria are concrete
// and enumerated, and nothing reaches a person outside the team until a human
// approval step records a decision. No step in any of them sends, posts or
// schedules anything.
//
// Their check instructions all enumerate lettered failure criteria ("FAIL if
// any of these is true: (a) …; (b) …"). That is a convention the seed lint in
// sdk_recurring_templates_test.go enforces, because "review the document" is
// how a review step quietly becomes a second opinion about tone.

func competitorBriefTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("competitor-brief",
		"Monday competitor brief — find what a named set of competitors changed inside one declared week, write a brief of at most 400 words covering three changes, have a separate model conversation check every claim against its source, and hold the email text until a person approves it. Nothing is sent.",
		`Prepare a competitor brief covering one declared week.
Competitors to watch: {slot:competitors}
Week the brief covers: {slot:weekWindow}
Who reads it: {slot:audience}
Handover: {slot:handover}
Report only changes you can point at with a source URL published or observably updated inside the declared week. A change you cannot source is a gap to report, never a sentence to write. Carry dates exactly as displayed and write "not shown" when a page shows none. Nothing here sends, posts or schedules anything: the last step writes email text that a person sends.`,
		[]store.ContextSlot{
			{ID: "competitors", Label: "Competitors to watch", Required: true, Type: store.SlotTypeList,
				Hint:    "The competitors you want watched this week, one per entry, named the way their own site names them so the research can find them. Add a product URL after a name when several companies share it. Whatever you leave out is not watched: this list is the entire scope of the brief.",
				Example: "Toggl, Harvest, Clockify"},
			{ID: "weekWindow", Label: "Week the brief covers", Required: true,
				Hint:    "The window a change must fall inside to count as this week's news. Write it as a rule rather than two fixed dates if you rerun this every Monday, so the saved workflow still means the right week next month. Anything published outside the window is reported as older context, not as a change.",
				Example: "the last complete Monday–Sunday week before the run date"},
			{ID: "audience", Label: "Who reads the brief", Required: true,
				Hint:    "The people who open this on Monday, what they already know, and what they decide. \"Why it matters\" is written for them and not for you, so name them precisely enough that the brief can skip what is obvious to them and spell out what is not.",
				Example: "the four people on our go-to-market team; they know our product well and the competitors barely"},
			handoverSlot("brief", "the email text for the #gtm-weekly list, ready for me to send"),
		},
		sdkworkflow.Node{ID: "research", Kind: "research", Title: "Find what changed this week", Instruction: `Search for changes each declared competitor made inside the declared week window. Keep the search bounded: at most three search or page-read rounds with batched queries, then report gaps instead of continuing. A change is a pricing move, a shipped feature, a positioning or packaging change, a funding or hiring signal, or a published policy change. For every candidate record: the competitor; one sentence saying what changed; the exact source URL; the publication or update date as displayed, or "not shown"; and whether that date falls inside the declared window. Never infer a date from a URL, a changelog position or a screenshot. Quote at most one short sentence per source. Record every competitor where nothing was found as "no change found" — an empty week for a competitor is a real finding, not a reason to reach for older news.`},
		sdkworkflow.Node{ID: "brief", Kind: "document", Title: "Write the brief", Instruction: `Using research.md, write the Monday brief for the declared audience in at most 400 words of body text. Choose exactly three changes — the three that most affect what this audience decides — and for each give: what changed, in one sentence; why it matters to this audience specifically; and one reaction the team could consider this week, written as an option nobody has decided on. Carry each change's source URL and its displayed date, with "not shown" where none was displayed. Name the competitors where nothing was found and say so plainly. Lead with the most consequential change. Define any term the audience would not already use. Do not add a fourth change, a summary of the market, or advice the sources do not support.`},
		sdkworkflow.Node{ID: "review", Kind: "check", Title: "Check every claim against its source", Instruction: `Read research.md and brief.md, then open the cited pages with live search. FAIL if any of these is true: (a) a change in the brief has no source URL, or cites a URL that is not in research.md; (b) a cited page does not say what the brief says it says — a differing price, date, feature name or scope is a failure, not a rounding; (c) the body runs longer than 400 words, which you must count rather than estimate; (d) a change whose displayed date falls outside the declared week is presented as this week's change; (e) the brief carries fewer or more than three changes; (f) a suggested reaction is written as a decision already taken; (g) a competitor with no change found is missing from the brief. A page you cannot open is a finding naming the URL, never a silent deletion. A pass means every sentence traces to a source that exists and says so; it does not mean the change matters, and it cannot see what a competitor did without publishing it.`},
		sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Approve the brief before the email is written", Instruction: `Read the brief and the check findings. Approve if you are willing to have these three changes go to the declared audience over your name; reject if a change is wrong, missing, or not worth their Monday. Approving records your decision on these documents and unblocks the final email text. It sends nothing — no message leaves this workflow at any step.`},
		sdkworkflow.Node{ID: "email", Kind: "document", Title: "Write the final email text", Instruction: `Write the email exactly as it should go out to the declared handover recipient: a subject line naming the week and the single most consequential change, then the approved brief as the body, with each change's source URL beside it. Change nothing the approval covered — no new claims, no softened caveats, no fourth change. If the handover names no recipient, address it to nobody in particular and say at the top which list it was written for. This step writes text. Sending it is a person's action, taken outside this workflow.`},
	)
}

func kpiCommentaryTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("kpi-commentary",
		"Monthly KPI commentary with source ledger — turn a KPI export into a ledger where every figure names the export row it came from, add external context for the two largest movers, write a one-page commentary that cites ledger rows, and hold it for a person's sign-off.",
		`Write the monthly KPI commentary from a declared export.
Month: {slot:month}
KPI export: {slot:exportPath}
What each KPI means: {slot:kpiDefinitions}
Last month's note: {slot:priorNote}
Every number in the commentary comes from the ledger this run builds, and every ledger row names the export row behind it. Compute each KPI exactly as its declared definition says, and never compare two figures whose windows differ. Read the export; never modify it. Nothing is sent or published.`,
		[]store.ContextSlot{
			{ID: "month", Label: "Month the commentary covers", Required: true,
				Hint:    "The reporting month, with its first and last day and whether both are included. Write the dates out rather than saying \"last month\": the day you run this is not the period you are reporting on, and every figure is selected by this window.",
				Example: "September 2026, 2026-09-01 through 2026-09-30 inclusive"},
			{ID: "exportPath", Label: "KPI export", Required: true,
				Hint:    "The absolute path of the CSV you exported, on the machine this runs on. Export it yourself before each run: this workflow reads a file and never queries your warehouse, so whatever is in the file is the whole evidence base. A file that does not contain the month above is reported as a gap rather than filled in.",
				Example: "/absolute/data/kpi-2026-09.csv"},
			{ID: "kpiDefinitions", Label: "What each KPI means", Required: true, Type: store.SlotTypeTable,
				Hint:    "One row per KPI you want commented on. The definition is the one your team argues about — say which rows count and which are excluded. The unit and the denominator are what stop a percentage from being read as a count. A KPI missing from this table is not commented on.",
				Example: `[{"kpi":"activation rate","definition":"Accounts that finished setup within 7 days of signing up, excluding internal accounts","unit":"percent","denominator":"accounts that signed up inside the month"},{"kpi":"paid signups","definition":"Signups with a completed first payment in the month","unit":"count","denominator":"not a ratio"}]`,
				Columns: []store.SlotColumn{
					{ID: "kpi", Label: "KPI", Required: true, Hint: "The name your team calls it by."},
					{ID: "definition", Label: "Definition", Required: true, Hint: "Which rows count, and which are excluded."},
					{ID: "unit", Label: "Unit", Required: true, Options: []string{"count", "percent", "currency", "ratio", "days", "other"}},
					{ID: "denominator", Label: "Denominator", Required: true, Hint: "What the figure is out of — \"accounts that signed up inside the month\". Write \"not a ratio\" for a plain count."},
				}},
			{ID: "priorNote", Label: "Last month's note", Required: false,
				Hint:    "Paste last month's commentary, or just the line you want carried forward, and this month's commentary will say what happened to it. Leave it blank the first time you run this: the commentary then simply makes no comparison to a previous month rather than inventing one.",
				Example: "August: activation dipped to 31% after the signup form change; we said we would look again in September."},
		},
		sdkworkflow.Node{ID: "ledger", Kind: "task", Title: "Build the figures ledger", Instruction: `Read the declared export and write two files into the run folder. "ledger.csv" carries one row per figure the commentary may use, with the columns figure_id, kpi, value, unit, denominator, window (the exact dates the value covers), source_row (the line number or key of the export row it was read from) and how_computed (the declared definition, as applied). "ledger.md" explains in plain sentences how each figure was produced, which export rows were excluded and why, the export's total row count and its date range. Compute every KPI exactly as its declared definition states. Where the export cannot support a declared definition, write the ledger row with the value "not available" and say in how_computed what the export is missing. Never estimate, interpolate, or carry a figure over from another period. These two files are the only source the later steps may cite, so a figure that is not in the ledger cannot appear in the commentary.`},
		sdkworkflow.Node{ID: "context", Kind: "research", Title: "Find external context for the two largest movers", Instruction: `Read ledger.md and ledger.csv. Identify the two figures that moved most against the comparison window the ledger records for them, judged in the declared unit rather than by eye. For each, search for external context inside the same window that could explain or contradict the move: a market, seasonal, regulatory or competitor event. Keep the search to at most three rounds. Record every item with its source URL and displayed date, or "not shown", and label it explicitly as either evidence of a cause or something that merely happened at the same time. Two figures, not five. Where nothing relevant is found, write that: an absent explanation is a better answer than a plausible one.`},
		sdkworkflow.Node{ID: "commentary", Kind: "document", Title: "Write the one-page commentary", Instruction: `Write at most one page for the person who owns these numbers. Open with the month and the single figure that changed most. Then cover: what each declared KPI did, citing its figure_id after the number and carrying its unit and denominator; the two largest movers with the context from context.md, keeping cause and coincidence separately labelled; what did not change, where saying so is worth the line; and what last month's note said would be watched, when one was supplied. Compare a figure only against a window the ledger records for that same figure. Define each KPI on first use in the reader's words. End with what these numbers cannot tell the reader, listing every ledger row whose value is "not available" and what it would take to fill it.`},
		sdkworkflow.Node{ID: "review", Kind: "check", Title: "Check every figure against the ledger", Instruction: `Read ledger.csv, ledger.md, context.md and commentary.md, and re-read the declared export. FAIL if any of these is true: (a) a number in the commentary carries no figure_id, or names a figure_id that is not in ledger.csv; (b) a ledger row is missing its source_row, its unit or its window; (c) a commentary number differs from its ledger value, or is rounded without saying so; (d) two figures are compared whose windows differ, including a month-on-month comparison the ledger does not record; (e) a KPI was computed differently from its declared definition, or its exclusions were dropped; (f) external context is presented as a cause where context.md recorded it as coincident, or carries no source URL; (g) a "not available" ledger row is missing from the commentary's limits. Recompute at least the two largest movers from the export yourself and say what you got. A pass means every figure traces to a row of the export and the arithmetic holds; it does not mean the export is correct, complete or current, and it is not an audit of the systems that produced it.`},
		sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Sign off on the commentary", Instruction: `Read the commentary, the ledger and the check findings — in particular the figures marked "not available" and anything the check recomputed. Approve if you accept this as the month's commentary; reject if a definition was applied differently from how your team means it, or a comparison is one you would not stand behind. Approving records your decision on these documents. It publishes nothing, sends nothing, and changes no number in your warehouse.`},
	)
}

func plainLanguagePassTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("plain-language-pass",
		"Plain-language pass for a technical document — restate in one sentence what a document is for, rewrite it for a named reader, and have a separate model conversation fail the rewrite on undefined terms, unitless numbers, weakened caveats and buried consequences. The review criteria are the technical-editor persona's audit protocol.",
		`Rewrite one technical document in plain language for a named reader.
The document: {slot:source}
Who has to understand it: {slot:audience}
Terms and their plain meanings: {slot:glossary}
Rewrite the supplied text and nothing else. Preserve every caveat, limit, failed check and number at its original strength: a clearer sentence that weakens a warning is a worse document, not a better one. Add nothing the source does not say and drop nothing the reader needs. Nothing is published, sent or written back over the original.`,
		[]store.ContextSlot{
			{ID: "source", Label: "The document to rewrite", Required: true,
				Hint:    "Paste the text, or give the absolute path of the file on the machine this runs on. Only what you supply here is rewritten: a link to a page nobody can open leaves the run with nothing to work from, and a path that does not exist is reported as a gap rather than guessed at.",
				Example: "/absolute/docs/release-notes-2026-09.md"},
			{ID: "audience", Label: "Who has to understand it", Required: true,
				Hint:    "Who must follow this after one read, what they already know, and what you need them to do with it. The rewrite puts that action where they will see it, and defines exactly the terms this group would not already use.",
				Example: "support agents answering customer questions about this release; they know the product well, the database not at all"},
			{ID: "glossary", Label: "Terms and their plain meanings", Required: false, Type: store.SlotTypeTable,
				Hint:    "Terms your team has already agreed how to say, and the wording to use for each. Fill this in and the rewrite uses your words instead of inventing a synonym. Leave it empty and every term of art is simply defined the first time it appears.",
				Example: `[{"term":"idempotent retry","plainMeaning":"trying again is safe — it will not charge the customer twice"},{"term":"backfill","plainMeaning":"filling in past days that were missed"}]`,
				Columns: []store.SlotColumn{
					{ID: "term", Label: "Term", Required: true, Hint: "The word as it appears in the document."},
					{ID: "plainMeaning", Label: "Plain meaning", Required: true, Hint: "How you want it said to this reader."},
				}},
		},
		sdkworkflow.Node{ID: "rewrite", Kind: "document", Title: "Restate the purpose, then rewrite", Instruction: `Begin with one sentence — no more — saying what this document is for, what outcome it reports or promises, and what the reader should do next. If the source does not let you write that sentence honestly, say which of the three is missing; that is often the most useful thing this workflow produces, so do not paper over it. Then write the rewrite for the declared audience: lead with the outcome and the reason that controls it; one idea per sentence; active voice with a named actor for every action; every term of art and abbreviation defined the first time it is used, in the supplied glossary's wording where one is given; every number carrying its unit and the population it is out of; every consequence for the reader placed before the implementation detail that causes it. Keep every caveat, limit, failed check and uncertainty at its original strength, and where the reader reaches it before acting rather than after. Do not add facts, soften a warning, or drop a condition to make a sentence shorter.`},
		sdkworkflow.Node{ID: "review", Kind: "check", Title: "Review the rewrite as a technical editor", Instruction: `Read the source and rewrite.md side by side. First write your own one-sentence restatement of purpose, outcome and next step using the rewrite alone; if you cannot write it, or it would be wrong, that is the controlling finding. FAIL if any of these is true: (a) a term of art or abbreviation is used before it is defined in this document; (b) a number appears without its unit, or a rate or percentage without the population it is out of; (c) a caveat, limit, failed check or uncertainty in the source is weakened, generalised or dropped — quote both versions; (d) a consequence for the reader appears after the implementation detail that causes it; (e) the opening does not state the outcome and the reason that controls it; (f) the rewrite asserts something the source does not support, or drops a condition the reader must meet; (g) a supplied glossary term is rendered in different words than the glossary declares. Quote the exact passage behind every finding and supply replacement wording that preserves the underlying claim. A pass means a first-time reader can restate what the document is about, what happened and what to do next; it does not mean the work the document describes is correct, or that the source was right about it. This is a judgment about wording only.`},
		sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Approve the rewrite before it replaces anything", Instruction: `Compare the rewrite with your original and read the editor's findings. Approve if this wording can go to the declared audience over your name; reject if any caveat now reads weaker to you than it did before, or the rewrite says something you did not mean. Approving records your decision on these documents. It does not publish the rewrite, overwrite the source file, or notify anyone.`},
	)
}

func customerFeedbackDigestTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("customer-feedback-digest",
		"Monthly customer feedback digest — group one month of exported feedback into themes with counted rows and verbatim quotes, fail the digest on paraphrased quotes, unreproducible counts or personal data, and write three recommendations only after a person approves the themes.",
		`Digest one month of customer feedback from a declared export.
Feedback export: {slot:exportPath}
Month: {slot:month}
Product area: {slot:productArea}
Handover: {slot:handover}
Quotes are copied from the export character for character, never paraphrased or tidied. Counts come from counting rows, never from an estimate. Remove personal data from every quote before it is written down. Read the export; never modify it. Nothing is sent to customers or to anyone else.`,
		[]store.ContextSlot{
			{ID: "exportPath", Label: "Feedback export", Required: true,
				Hint:    "The absolute path of the file to read — the CSV or text export your feedback tool produced, on the machine this runs on. Export it yourself before each run: this workflow reads a file and never connects to the tool, so whatever is in the file is the whole evidence base.",
				Example: "/absolute/data/feedback-2026-09.csv"},
			{ID: "month", Label: "Month the digest covers", Required: true,
				Hint:    "The month whose feedback you want digested, with its first and last day and whether both are included. Rows outside this window are reported as out of scope rather than quietly folded in, so a stale export shows up as a gap instead of as a theme.",
				Example: "September 2026, 2026-09-01 through 2026-09-30 inclusive"},
			{ID: "productArea", Label: "Product area", Required: false,
				Hint:    "One area to narrow the digest to, when you only want that part. Say how a row is recognised as belonging to it — a column value, a tag, or words to look for — because the export rarely says so by itself. Leave this blank and every row inside the month is considered.",
				Example: "the mobile app: rows whose area column is \"mobile\", or whose text mentions iOS or Android"},
			handoverSlot("digest", "a markdown digest for the #product-feedback list"),
		},
		sdkworkflow.Node{ID: "themes", Kind: "document", Title: "Group the feedback into themes", Instruction: `Read the declared export and group the rows inside the declared month — and inside the declared product area, when one is given — into themes. A theme is something several customers ask for or struggle with, named in their words rather than yours. For each theme give: the name; the count of rows in it, obtained by counting rows and never by estimating; at least two quotes COPIED CHARACTER FOR CHARACTER from the export, each with the row identifier or line number it came from; and one sentence about what those customers were trying to do. Redact personal data from every quote before writing it: names, email addresses, phone numbers, account and order identifiers, and any company name that identifies a single customer, each replaced with [redacted] and never with a plausible substitute. A quote that cannot be redacted without destroying its meaning is dropped in favour of another row. Report the export's total row count, the rows inside the window, the rows excluded and why, and the count of in-window rows that fit no theme. A concern supported by a single row is listed separately as a single report, not promoted into a theme.`},
		sdkworkflow.Node{ID: "review", Kind: "check", Title: "Check quotes and counts against the export", Instruction: `Read the declared export and themes.md. FAIL if any of these is true: (a) a theme carries fewer than two quotes whose text is found verbatim in the export; (b) a quote differs from its export row in any way other than a [redacted] replacement or a marked ellipsis — a tidied or paraphrased quote is a fabricated quote; (c) a quote has no row identifier, or its identifier points at a different row; (d) a theme's count cannot be reproduced by counting the rows assigned to it; (e) the totals do not add up: rows in window, rows excluded and rows fitting no theme must reconcile with the export's row count; (f) any quote still contains a name, an email address, a phone number, an account or order identifier, or a company name identifying one customer; (g) a themed row falls outside the declared month, or outside the declared product area when one was given. Recount at least the two largest themes yourself and report the numbers you got. A pass means the quotes are real and the counts add up; it does not mean the export represents your customers, that a loud theme is an important one, or that silence about a problem means it does not exist.`},
		sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Approve the themes before recommendations are written", Instruction: `Read the themes, their counts and the check findings. Approve the set of themes you accept as this month's picture; reject if a theme is an artefact of how rows were grouped, or if something you know matters is buried in the unthemed rows. Approving records your decision on these documents and unblocks the recommendation step, which may then use only the themes you approved. Nothing is sent to customers or to anyone else at any point.`},
		sdkworkflow.Node{ID: "recommendations", Kind: "document", Title: "Write three recommendations", Instruction: `Write exactly three recommendations for the declared handover reader. Each one names the approved theme it rests on, that theme's count, at least one of its quotes, what you propose doing, and what would show afterwards that it worked. Order them by how many customers the underlying theme affects, and say where you departed from that order and why. Stay inside what the feedback supports: a recommendation that needs evidence this export does not contain is written as the question to answer first, not as a plan. Introduce no theme the approval did not cover. If the handover names no recipient, address the document to nobody in particular. This step writes a document; acting on it, and sending it, are people's decisions outside this workflow.`},
	)
}

// fileLandingAuditTemplate profiles a file that is supposed to land every
// period. Its profiling step wants a read-only grant on the folder the file
// lands in and NO network hosts at all, which the node validator does not yet
// accept (an Access block with an empty Hosts list is rejected). Until that
// lands the step carries no Access block and relies on the paths the runtime
// already exposes to a task step; docs/workflows/file-landing-audit.md carries
// the workaround. Do not add an unrelated host here to make a grant parse.
func fileLandingAuditTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("file-landing-audit",
		"Monthly file landing audit — measure the file that is supposed to land each period, fail the audit when the row count falls outside the range you declared or when a figure was not measured, and hand over a scorecard after a person signs off. Nothing is sent.",
		`Audit a file that is supposed to land each period.
File: {slot:filePath}
Rows expected: {slot:expectedRows}
Period: {slot:period}
Key columns: {slot:keyColumns}
Handover: {slot:handover}
Measure the file itself; never modify it and never write into its folder. Every number in this audit is read from the file — a count inferred from a column name, a file size or a previous period is a fabrication. A missing, empty or unreadable file is this audit's finding, not a reason to profile a different file.`,
		[]store.ContextSlot{
			{ID: "filePath", Label: "The file to audit", Required: true, Audience: store.SlotAudienceEngineer,
				Hint:    "The absolute path of the file this audit measures, on the machine the run executes on — ask whoever runs the drop if you do not know where it lands. One file per run: if the drop arrives as a folder of parts, name the part this audit is about or concatenate them first, because a pattern that matches nothing looks exactly like a file with no rows.",
				Example: "/absolute/data/landing/orders-2026-09.csv"},
			{ID: "expectedRows", Label: "Rows you expect", Required: true,
				Hint:    "The row range a healthy drop falls in, written as min–max: the counts you would not be alarmed by. The check FAILS when the measured count falls outside it, so set it from the last few periods rather than from hope. Count data rows, not the header line.",
				Example: "1200000–1600000"},
			{ID: "period", Label: "Period this drop covers", Required: true,
				Hint:    "The period you expect this file to cover, with its first and last day. The audit compares the dates it finds inside the file against what you declare here, so a drop that is a month stale shows up as a finding instead of as a healthy row count.",
				Example: "September 2026, 2026-09-01 through 2026-09-30 inclusive"},
			{ID: "keyColumns", Label: "Key columns", Required: true, Type: store.SlotTypeList, Audience: store.SlotAudienceEngineer,
				Hint:    "The columns that should together identify one row, so the audit counts how often they are missing or repeated instead of taking it on trust. Ask whoever runs the pipeline if you do not know. If this file has no natural key — no set of columns guaranteed unique — write `none` first and then the closest candidates (`none, order_id, source_system`); that is a real answer, and the audit reports whether the candidates actually hold.",
				Example: "none, order_id, source_system"},
			handoverSlot("scorecard", "a markdown scorecard for the #data-eng-oncall channel"),
		},
		sdkworkflow.Node{ID: "profile", Kind: "task", Title: "Profile the landed file", Instruction: `Measure the declared file and write two files into the run folder. "counts.json" records: the absolute path as read; the file's size in bytes and its modified timestamp; the header as found; the data row count excluding the header; a null-and-blank count for EVERY declared key column; the number of rows whose declared key columns repeat a combination already seen; and, for every column holding a date or timestamp, its minimum and maximum value as found together with the format they were parsed in. "profile.md" says in plain sentences what was measured, the commands or code actually run, how the row count sits against the declared expected range, whether the dates inside the file fall inside the declared period, and what could not be measured and why. Measure the whole file. If you must sample, say so, state the sample size and how it was drawn, and never present a sampled figure as a full-file count. If the file is missing, empty or unreadable, record that as the result together with the exact error, and substitute nothing.`},
		sdkworkflow.Node{ID: "review", Kind: "check", Title: "Check the profile against the declared expectations", Instruction: `Read counts.json, profile.md and the commands that produced them. FAIL if any of these is true: (a) the measured data row count falls outside the declared expected range — report the measured count, the range, and the direction it missed by; (b) counts.json lacks a null-and-blank count for any declared key column; (c) any value is a placeholder, a TODO, a suspiciously round figure with no measurement behind it, or a number reasoned from a column name rather than read from the file; (d) a date or timestamp column has no minimum and maximum, or the dates found fall outside the declared period without profile.md reporting that as a finding; (e) the file was sampled without stating the sample size and how it was drawn, or a sampled figure is presented as a full-file count; (f) the repeated-key-combination count for the declared key columns is missing, or the candidates given after "none" were never tested; (g) the file was missing or unreadable and the profile reports counts anyway. A pass means this drop looks like the drops you declared you expect; it does not mean the values are correct, that the upstream job ran, or that the rows mean what their column names say.`},
		sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Sign off before the scorecard is handed over", Instruction: `Read counts.json, profile.md and the check findings: the row count against your range, the null and repeated-key counts for the key columns, and the dates found inside the file. Approve when you accept this drop as landed; reject when it needs upstream attention before anyone builds on it. This step runs every period, not only when the counts drift — the workflow has no conditional steps, so a clean period is a quick approval and a drifting one is where you stop the line. Approving records your decision on these documents. It changes nothing upstream and sends nothing.`},
		sdkworkflow.Node{ID: "scorecard", Kind: "document", Title: "Write the scorecard", Instruction: `Write the scorecard for the declared handover reader, short enough to read on one screen. Open with the verdict: did this period's file land inside the range you declared, yes or no. Then a table carrying rows measured, expected range, nulls and blanks per key column, repeated key combinations, date minimum and maximum, file size and modified time. Follow it with what changed against the declared expectations, what could not be measured, and the one thing the reader should do next — "nothing" is the right answer when nothing is wrong. Carry the check's findings forward at their original strength: a scorecard that reads clean while the row count sat outside the range is a false report. If the handover names no recipient, address it to nobody in particular. This step writes a document; posting it to a channel is a person's action outside this workflow.`},
	)
}
