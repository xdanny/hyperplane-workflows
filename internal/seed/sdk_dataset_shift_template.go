package seed

import (
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func datasetShiftTemplate() store.WorkflowTemplate {
	return sdkNodeTemplate("dataset-shift-audit", "Compare two measured data windows using shared bins, feature-specific tolerances, fixed controls and your approval before execution. Every step is editable SDK work.",
		`Compare these data windows without modifying either source.
Baseline: {slot:baselineData}
Current: {slot:currentData}
Features and their owner-declared tolerances: {slot:features}
Controls requested as multiples of those tolerances: {slot:seededControl}
Key columns: {slot:keyColumns}
Safe use: {slot:safeUse}
Runtime budget: {slot:runtimeBudget}
Execution environment: {slot:environment}
Handover: {slot:handover}
Blank optional values mean unspecified. Configure connections and tool setup explicitly on the profile and audit steps when needed; prose grants neither. Produce local findings and requested handover files, without sending or publishing.`,
		[]store.ContextSlot{
			{
				ID:       "baselineData",
				Label:    "Baseline window",
				Hint:     "The window you are comparing AGAINST — where it lives, the period it covers, format and rough size — and why it is the right reference (usually: it is what the model was trained on, or what the report was last trusted over).",
				Required: true,
				Example:  "data/yellow_tripdata_2025-01.csv … 2025-03.csv (~3M rows/month) — the quarter the fare model was fitted on",
			},
			{
				ID:       "currentData",
				Label:    "Current window",
				Hint:     "The window under test — same shape as the baseline, or the comparison measures your extraction rather than the world. Where it lives, the period it covers, format and rough size.",
				Required: true,
				Example:  "data/yellow_tripdata_2025-04.csv … 2025-06.csv (~3M rows/month, same TLC schema)",
			},
			{
				// One row per feature, with its own tolerance and its own unit.
				// The two prose boxes this replaces were "Features that matter,
				// and why" and "How much movement is too much": N features in
				// one paragraph and one threshold in another, so a feature with
				// no threshold of its own quietly borrowed another feature's —
				// which is what happened, was approved inside 22.7 KB of JSON,
				// and was then repaired by inventing 1e-12.
				ID:       "features",
				Label:    "Features that matter, and how much movement is too much",
				Hint:     "One row per column you want watched. The 'why' is what turns a drift number into a decision; the tolerance is YOUR number, declared before anything is measured, and it is never borrowed from another row. Every categorical feature needs one — the run is refused without it, rather than the workflow picking a number for you.",
				Required: true,
				Type:     store.SlotTypeTable,
				Columns: []store.SlotColumn{
					{ID: "feature", Label: "Feature", Hint: "The column name, exactly as it appears in both windows.", Required: true},
					{ID: "type", Label: "Type", Hint: "numeric, categorical or timestamp — it decides which statistic can be computed.", Required: true,
						Options: []string{"numeric", "categorical", "timestamp"}},
					{ID: "whyItMatters", Label: "Why it matters", Hint: "What breaks downstream when this column moves. A severity with no consequence behind it is a mood, not a severity.", Required: true},
					{ID: "severity", Label: "Severity", Hint: "blocker, major or minor — how loudly a breach should land.",
						Options: []string{"blocker", "major", "minor"}},
					{ID: "statistic", Label: "Statistic", Hint: "PSI (population stability index) and KL (Kullback–Leibler divergence) score how far a distribution moved, 0 meaning no change. Choose the statistic for this feature. Category-emergence detects values that appeared or vanished; it is optional when you select PSI or another statistic.",
						Options: []string{"psi", "kl", "rate-delta", "category-emergence", "volume-drift"}},
					{ID: "tolerance", Label: "Tolerance", Hint: "The number at or above which this counts as shift. The usual PSI convention is 0.2; a categorical emergence limit is usually stated in points of share.", Required: true},
					{ID: "unit", Label: "Unit", Hint: "What that number is measured in. PSI points, points of share, a percentage of the baseline, or an absolute count — a tolerance without its unit is not a tolerance.", Required: true,
						Options: []string{"psi", "kl", "points_of_share", "percent_of_baseline", "absolute", "count"}},
					{ID: "watchBand", Label: "Watch band", Hint: "Where this becomes worth watching without yet being worth acting on. Optional, and always below the tolerance. Convention for PSI: 0.1."},
					{ID: "owner", Label: "Set by", Hint: "Who stands behind this number. It is quoted back to them when the rule is approved."},
				},
				Example: `[{"feature":"trip_distance","type":"numeric","whyItMatters":"the fare model's strongest predictor","severity":"blocker","statistic":"psi","tolerance":"0.2","unit":"psi","watchBand":"0.1","owner":"Dev, ML lead"},{"feature":"payment_type","type":"categorical","whyItMatters":"a new code silently breaks the card/cash split","severity":"major","statistic":"category-emergence","tolerance":"2","unit":"points_of_share","owner":"Dev, ML lead"}]`,
			},
			{
				// D-11: the control that proves the detector works was designed
				// by the node it tests — "a stated share above the emergence
				// tolerance", chosen by the author of the rules it validates. A
				// control sized above the tolerance by its own author always
				// trips. The owner sizes it here instead, and a separate node
				// injects it.
				ID:       "seededControl",
				Label:    "The distortion the checks must be able to catch",
				Hint:     "A deliberate, known movement injected into a COPY of the current window, which the same rules must then trip on. You size it as a multiple of that feature's own tolerance — 1.25× means 'a quarter over the line'. Leave it empty and every categorical feature is controlled at 1.25×, which is recorded on the scorecard as the platform's choice rather than yours.",
				Required: false,
				Type:     store.SlotTypeTable,
				Columns: []store.SlotColumn{
					{ID: "feature", Label: "Feature", Hint: "One of the features above.", Required: true},
					{ID: "multiple", Label: "Multiple of its tolerance", Hint: "1.25 injects a movement a quarter over that feature's declared tolerance. Below 1 the control is inside the tolerance and must NOT trip.", Required: true},
				},
				Example: `[{"feature":"payment_type","multiple":"1.25"}]`,
			},
			{
				ID:       "keyColumns",
				Label:    "Key columns",
				Hint:     "The columns that identify one row in BOTH windows — what a row in the baseline is lined up against in the current window when a feature is compared. If this data has no natural key — no set of columns guaranteed to be unique — write `none` first and then the closest candidate columns (`none, VendorID, pickup_time, PULocationID`). That is a real answer, not a blank: the workflow treats the candidates as the closest thing to a key and reports whether it actually holds.",
				Required: true,
				Type:     store.SlotTypeList,
				Audience: store.SlotAudienceEngineer,
				Example:  "none, VendorID, tpep_pickup_datetime, PULocationID",
			},
			{
				ID:       "safeUse",
				Label:    "What is confidential here, and what these numbers must not be used for",
				Hint:     "Sensitive columns and how they may appear in the scorecard (bucket ranges, counts only, masked), and what these findings must NOT be used for.",
				Required: true,
				Example:  "no direct identifiers; histograms shown as bucket ranges only; findings are for model and report triage, not for judging individuals",
			},
			// M-4: this was the only data recipe with neither a runtime budget
			// nor an environment slot, so the one audit that reads two full
			// windows end to end was also the one that could not be told how
			// long it had or where it was running. Both inherit from the
			// project, so a project that has answered them once answers them
			// here too.
			{
				ID:       "runtimeBudget",
				Label:    "Runtime budget",
				Hint:     "How long profiling both windows and computing every rule may take before the run should fail loudly rather than grind on. Leave blank and no budget is imposed; fill it in and the checks script enforces it — never by profiling a sample of a window and reporting it as the window.",
				Required: false,
				Example:  "both windows profiled and every rule computed inside 20 minutes",
			},
			{
				ID:       "environment",
				Audience: store.SlotAudienceEngineer,
				Label:    "Where the audit runs",
				Hint:     "The machine or container the comparison itself runs on, the engine or interpreter available there, whether package installs can reach the network, and where its outputs land. Blank means the workflow may assume its own workspace.",
				Required: false,
				Example:  "local laptop, duckdb via a workspace venv; the scorecard lands in the run's artifacts and neither window is written to",
			},
			handoverSlot("scorecard", "a markdown scorecard plus the per-feature CSV, to #ml-monitoring"),
		},
		sdkworkflow.Node{ID: "profile", Kind: "task", Title: "Measure both windows", Instruction: "Measure both declared windows using shared bins and owner-declared features. Preserve source identities, exact tolerances and scan scope. Follow the built-in profile contract; the SDK supplies fixed control specifications.", Acceptance: "dataset-shift-profile", Inputs: map[string]string{"baselineData": "{slot:baselineData}", "currentData": "{slot:currentData}", "features": "{slot:features}", "keyColumns": "{slot:keyColumns}", "seededControl": "{slot:seededControl}"}},
		sdkworkflow.Node{ID: "profile-review", Kind: "check", Title: "Review comparability and measurements", Instruction: "BOTH windows are profiled with MEASURED numbers: each carries a row count on the order of its declared size, the period it actually covers observed from the data, and a scan mode. EVERY feature named in the brief appears exactly once in features[] with a distribution for BOTH windows, or comparable:false with a concrete schema difference and reason. A silently missing feature or invented distribution is a fail. The binning is SHARED: numeric features state that the current window was bucketed into the baseline's bin edges, and a profile that re-cut bins independently per window is a fail because it hides the movement it claims to measure. Categorical features carry the full value vocabulary with counts, so an emerged or vanished value is visible. Volume is reported per period for both windows. Any schema difference between the windows is named as a schema difference rather than left to surface later as drift. Fail on placeholders, TODOs, round figures with no measurement behind them, distributions reasoned from column names instead of the data, or a sample scan that does not state its size and method for BOTH windows. If any column named sensitive in the safe-use boundary appears with raw values rather than bucket ranges, fail. Include the most consequential finding for the business in findings. Every count carries its denominator in plain words."},
		sdkworkflow.Node{ID: "rules", Kind: "task", Title: "Propose feature-specific shift rules", Instruction: "Propose drift rules from both measured windows, preserving each feature tolerance and unit. Include the selected statistic for all declared features and explain missing evidence. Additional emergence, volume or hazard rules are optional proposals with justified thresholds for approval. Follow the built-in rules contract; do not audit before approval.", Acceptance: "dataset-shift-rules"},
		sdkworkflow.Node{ID: "rules-review", Kind: "check", Title: "Review tolerances and coverage", Instruction: "EVERY feature named in the brief is either a rule in rules[] or an entry in unexpressible[] with a reason — a feature that appears in neither is a silently dropped promise and a fail. Every rule names an executable statistic (psi, kl, rate-delta, category-emergence, volume-drift) with the exact binning or vocabulary it uses, referencing the BASELINE bin edges rather than re-cutting per window, and a tolerance in the statistic's unit. The primary rule uses the unit that feature's row declared, carrying toleranceOrigin: \"declared\" only when the statistic, number and unit are exactly the ones on that feature's own row in the brief, \"proposed\" otherwise. A primary rule whose tolerance differs from its feature's declared number, or any rule copied from another feature's row or marked \"declared\" without a row behind it, is a FAIL - that substitution is the failure this check exists for, and it is invisible inside 20 KB of JSON unless something says so here. Require each comparable feature's selected statistic, preserving its exact declared tolerance and unit. Additional category-emergence, volume-drift or hazard rules are optional; their absence does not fail review. If proposed, their thresholds must be labelled proposed, justified by a downstream consequence and included in human approval. Every severity names a concrete downstream consequence — which model, report or decision breaks when that feature moves — rather than restating the rule. TOLERANCES MUST NOT BE PRE-TUNED TO PASS: a proposed tolerance derived from a measured statistic merely to hide a breach is a fail; an unchanged owner-declared tolerance remains valid even when the data is stable, and a rule the profile shows will trip must be marked expectedToTrip:true rather than accommodated. A column whose type, unit or encoding changed between the windows must be treated as a schema difference, not measured as drift. Include the most consequential finding for the business in findings. Every count carries its denominator in plain words.\nJudge proposed extra thresholds by their stated consequence and justification; a legitimately owner-declared threshold is not invalid merely because the measured value is lower. Check control units and positive-event definition match the rule. Reject claimed owner thresholds that differ from the declared feature row."},
		sdkworkflow.Node{ID: "approve-rules", Kind: "approval", Title: "Approve the rules and fixed controls", Instruction: "Human approves the shift rule set - its statistics, its binning and above all its tolerances - before anything is computed against the data and a number goes on the record. Each rule states whether its tolerance was DECLARED BY THE OWNER or PROPOSED BY THE AGENT, and a proposed one is a separate decision: approving the set must never be how a number nobody set becomes a number everybody trusts. The owner owns what 'too much movement' means; the audit only owns whether it happened.\nReview ../profile/control-spec.json and ../profile/control-counts.json alongside shift-rules.json. Fixed controls use synthetic weighted bins so small movements are not rounded away. Confirm declared versus proposed thresholds and owner versus platform-default control attribution."},
		sdkworkflow.Node{ID: "audit", Kind: "task", Title: "Execute the approved comparison", Instruction: "Execute the approved drift rules against the measured windows and the SDK-supplied fixed controls. Preserve exact owner tolerances, report actual denominators and distinguish shifts from evidence gaps. Follow the built-in scorecard contract.", Acceptance: "dataset-shift-audit"},
		sdkworkflow.Node{ID: "audit-review", Kind: "check", Title: "Review measured shift and control results", Instruction: "EVERY approved rule appears exactly once, either in rules[] with executed results or in notRun[] with a concrete reason — a rule that appears in neither is a silent drop and an automatic fail, and notRun is a failure of the audit rather than a neutral note. PER RULE the report carries {feature, statistic, checkedUnit, checkedBaseline, checkedCurrent, value, tolerance, shifted} with REAL numbers and the query that produced them: checkedUnit names what the checked counts count in the plural noun a reader would use (\"rows\", \"files\", \"columns\"), and a rule row with no checkedUnit — or one whose examples fabricate a row count for a file-grain rule, or which pluralises against the field name rather than the count (\"1 shifted bins\") — is a fail; both checked counts must be greater than zero and on the order of their window's declared size (a rule 'stable' over 0 rows on either side is a fail), rows excluded as null or unbinnable are reported rather than absorbed, and shifted must be the computed tolerance comparison — a shifted:false with no statistic behind it, or a verdict that contradicts its own numbers, is a fail. Every rule that reports movement carries its top contributing bins with each window's share, so the finding says WHICH part of the distribution moved. negativeControls[] carries one entry per control in control-spec.json, each naming the feature, the owner's multiple, the tolerance it multiplies, the target statistic and the value actually computed, with source \"sdk-control\" - a control the executing node sized for itself, or a control missing for a feature control-spec declares, is a fail. A control with expectTrip:true that did not trip is a failure of the whole audit however stable the real windows look. If windows.scanned is 'sample', both windows state their sample size and method and no statistic is presented as a full-population fact. Contributors and examples respect the safe-use boundary: no raw values from a column named sensitive there. Fail if the primary evidence came from the seeded control rather than the declared windows. Include the most consequential finding for the business in findings. Every count carries its denominator in plain words.\nControls come from ../profile and use fractional synthetic weights. Verify reported values by reading their actual weights; achieved claims are not a substitute for execution. Zero-tolerance rules are stable at zero."},
		sdkworkflow.Node{ID: "owner-review", Kind: "check", Title: "Review impact and the weakest rule", Instruction: "Judge this as the engineer who owns the model or report fed by this data and will be embarrassed if it degrades silently. PASS only if: both windows were profiled with shared binning and the rules were derived from those measurements and the owner's declared tolerances; every feature the brief named is either ruled on or explicitly listed as unexpressible; every approved rule was computed against the full declared windows with real per-window counts; the shifted verdicts follow from the numbers and the approved tolerances rather than from a tolerance moved to fit; the seeded control was sized from the owner's declared multiple by sdk-control and matched its expected verdict (including stable controls below the threshold); and the severities name real downstream consequences. FAIL if the audit is reassuring rather than informative: proposed tolerances chosen from measured statistics to hide a breach, statistics computed from two differently-drawn samples or from independently re-cut bins, a re-encoded column reported as drift instead of as a schema change, rules quietly missing from the scorecard, a seeded control that contradicts its expected verdict or that the executing node sized for itself, a rule carrying a tolerance marked \"declared\" that is not the number on that feature's own row, a blocker feature shifted while the summary reads as calm, or contributors that leak values the safe-use boundary forbids. Name the single most consequential thing this scorecard says about the current window — the one an owner must act on — and the weakest rule in the set. REVIEW SCOPE: judge ONLY against the declared context contract (the CONTEXT block: baseline window, current window, features and why they matter, tolerances, safe-use). FAIL only when a declared-contract item is unproven or contradicted by the artifacts: a declared feature neither ruled nor declared unexpressible, a rule not executed, a verdict contradicting its own counts, a control that contradicts its expected verdict, or a safe-use breach. Concerns OUTSIDE the declared contract — features nobody asked to watch, upstream fixes, retraining strategy, monitoring that would be nice to have — are advisory findings: include them in findings, but do NOT fail the gate for them. The human outcome decision, not this gate, weighs findings. Include the most consequential finding for the business in findings. Every count carries its denominator in plain words."},
		sdkworkflow.Node{ID: "summary", Kind: "document", Title: "Explain the findings and next actions", Instruction: "Write the shift summary the owner of the downstream model or report reads. Return a standalone Markdown report. The markdown must cover, in order: the headline — has this data moved enough to matter for the use the brief named, yes or no, in one sentence; the features that BREACHED their tolerance, each with its statistic, the tolerance it passed, WHICH part of the distribution moved (the top contributing bins in plain words) and what breaks downstream, stated first because that is the part the owner cannot get anywhere else; the full scorecard as a table (feature, statistic, baseline rows, current rows, value, tolerance, verdict); what is genuinely stable, so the owner knows what they can still rely on; the watch band — features that moved but not past the line, which is where the next window's story usually starts; what to do next, ordered by downstream consequence rather than by statistic size, each with a suggested owner; the features that could not be compared and what it would take to compare them; and the limits — what this audit did NOT look at, and specifically that a shift audit says the distribution moved, never WHY it moved: a real-world change, an upstream pipeline change and a collection artefact all look identical here. Respect the safe-use boundary in every example. Carry the reviewer's most-consequential finding forward verbatim rather than smoothing it over; a summary that reads as calm while a blocker feature breached its tolerance is a false report. STYLE CONTRACT — this document is read by people who were not in the run, so it is held to an editorial standard (personas/quinn-tech-editor.md): Write for a first-time reader. Lead with what happened and what to do next — the verdict in sentence one, never sentence four. Active voice; a named actor for every action. One idea per sentence. Define every term of art on first use. Every number carries its unit and denominator. Preserve every caveat and uncertainty — clarity never softens a finding. The test: a reader who was not present can restate what this workflow tried to do, what happened, and what to do next after one read. EXPLAIN ANY NUMBER THAT APPEARS IN TWO DIFFERENT FINDINGS: when the same figure shows up under two findings, say in one clause why it is the same number — a shared denominator, one population counted twice, or a genuine coincidence — because a reader who spots it and is not told assumes one of the two findings is a copy of the other. DISPLAY PRECISION: show percentages and points at two decimals, and where you shorten a value say that the scorecard carries it in full. HAND-OVER: the brief names how this scorecard is handed over — a format and a recipient reference. Address the scorecard to that recipient in its opening line and deliver it in that format. When the brief says the hand-over is unspecified, address it to nobody in particular: invent neither a recipient nor a format, because a document addressed to a guess is worse than one addressed to no one.\nReturn a standalone Markdown summary. Use negativeControls in the full scorecard, preserving every control and its owner/platform attribution; do not collapse them to one misleading boolean. Address the requested handover but do not send or publish."},
		sdkworkflow.Node{ID: "summary-review", Kind: "check", Title: "Review the final explanation", Instruction: "READABILITY IS PART OF THE DELIVERABLE — judge this document as a technical editor would, not as its author. PASS only if all five hold. (a) OPENING: the first two sentences state what this workflow attempted AND what the outcome was; a verdict that first appears in sentence three or later is a FAIL, however well written. (b) NO UNDEFINED JARGON: every term of art and every abbreviation is defined at first use, inside this document, without needing the run that produced it — quote each undefined term in findings. (c) NUMBERS CARRY THEIR MEANING: every quantitative claim states its unit AND its denominator; a bare count, a bare percentage, or a rate with no population is a FAIL — quote each offending number. (d) READER-RESTATEMENT TEST: Use the restatement field for your own single-sentence account of what the workflow tried to do, what happened, and what the reader should do next, using ONLY this document. If you cannot write that sentence from this document alone, or it would be wrong, that is the FAIL and the missing piece is the finding. A pass with an empty restatement is not a pass. (e) CAVEATS SURVIVE: every caveat, limit, uncertainty and failed or partial check carried by the upstream verdicts and reports still appears here at its original strength — a caveat softened, generalised or dropped to improve flow is a falsification and a FAIL. Rigor outranks readability where they collide. Quote the offending passage in findings for every failure so the repair knows exactly what to rewrite. Include the most consequential finding for the business in findings. Every count carries its denominator in plain words."},
	)
}
