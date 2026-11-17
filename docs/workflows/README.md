# Workflow templates

A template is a starting definition for the same capable SDK agent. It supplies
instructions, input fields and review criteria. It does not create a specialist
agent runtime. You can also start with your own steps and descriptions.

## Create, review and run

After `make seed`, open **Workflows → Start from a workflow template**, provide
its inputs and review the prepared steps. Edit instructions, reorder steps and
configure any required connections. Save for reuse or run the reviewed definition.
Preparing or saving SDK steps makes no model call and starts no work.

The same editor supports dragging, clicking to add, one description per line and
a reviewed suggestion from a goal. Described steps default to documents. Choose
research, independent review, approval or file-producing work where those behaviors
are needed. General work needs no acceptance command or domain-specific check;
extra checks can be selected explicitly. All currently supported sequences run in order; branching is not implemented.

| Starting template | Purpose | Current execution |
|---|---|---|
| [Market research](market-research.md) | Gather sources, rank problems, propose concepts | SDK steps |
| Trip planning | Research destinations, feasibility and itineraries | SDK steps |
| Deep research | Investigate perspectives and review evidence | SDK steps |
| Interview preparation | Research a role and use real career evidence | SDK steps |
| Code review | Inspect a named diff and review the findings | SDK steps |
| Finance review | Analyze supplied transactions and check calculations | SDK steps |
| [Data quality](data-quality-audit.md) | Profile data and execute approved rules | SDK steps with built-in contracts |
| [Dataset shift](dataset-shift-audit.md) | Compare windows against declared tolerances | SDK steps with built-in contracts |
| [Reconciliation](dashboard-reconciliation.md) | Explain differences between two sources | SDK steps with built-in contracts |
| [Pipeline change](pipeline-change.md) | Capture a baseline and verify a local change | SDK steps with built-in contracts |
| [Data pipeline](data-pipeline.md) | Build tables and reproduce declared measures | SDK steps with built-in contracts |
| [ML engineering](ml-engineering.md) | Build and assess a baseline model | SDK steps with an optional prediction check |
| [Monday competitor brief](competitor-brief.md) | Track named competitors across one declared week | SDK steps, approval before the email text |
| [KPI commentary](kpi-commentary.md) | Comment on a month's KPIs from a source ledger | SDK steps, approval before sign-off |
| [Plain-language pass](plain-language-pass.md) | Rewrite a technical document for a named reader | SDK steps, approval before it replaces anything |
| [Customer feedback digest](customer-feedback-digest.md) | Theme a month of feedback with verbatim quotes | SDK steps, approval before recommendations |
| [File landing audit](file-landing-audit.md) | Measure a periodic file drop against a declared range | SDK steps, approval every period |

Every shipped template prepares the common SDK editor definition through the
API or CLI. No process registration or graph draft is required. ML training uses
the same general task runtime; the optional prediction check recomputes submitted
rows and does not prove holdout custody or model provenance.

## Workflows you rerun every period

The last five templates in the table are built for repetition. What changes
between periods — the week, the month, the export path, the competitor list — is
an input rather than an edited instruction, so the definition you reviewed once
is the definition that runs again. Prepare one, review its steps and save it;
the saved workflow keeps its `{slot:…}` tokens, and
`hyperplane workflow run <name> --input k=v` fills them at launch. A missing
required input is refused with the input named, not run with a gap.

Each of them ends at a person: an approval step records a decision before
anything is written for an audience outside the run, and no step sends, posts or
schedules anything. Hyperplane has no scheduler, so rerunning on a timer is
something outside it does —
[examples/repeatable-workflows](../../examples/repeatable-workflows/README.md)
shows that loop and what it cannot promise.

## Inputs are owner facts

Name actual sources, row meanings, business definitions, tolerances and privacy
limits. Source descriptions do not grant access. Missing data must remain missing;
a plausible fixture cannot stand in for the requested source.

Project declarations can prefill template inputs through
`internal/store/declarations.go`. Review and edit those values for the current
run. This is convenience, not proof that a file is current or a connection works.
Store connection reference names, never credentials, in workflow definitions.

Table fields have declared columns. The editor stores canonical JSON and validates
required cells. SDK profile inputs retain structured JSON; prose context may use
a readable rendering. The form's hint should explain what a person must supply
and why; implementation details belong in the contract/source documentation.

## What a result means

A document check establishes nonempty, bounded output. Research requires an actual
search and a source link. Independent review is a separate model judgment against
stated criteria. Its findings are typed: the reviewer labels each one `FAIL —`,
`CAVEAT —` or `CONFIRMED —`, and the review document groups them under Failed,
Caveats and Confirmed. A passed review with three confirmations is not a review
with three caveats, and a pass that reports a failed criterion is malformed and
stops later steps. A human approval records a decision about linked evidence.
General work checks output presence and file integrity. Without an extra check,
completion is not independent verification of correctness. A selected acceptance
command or built-in check adds its specific validation. These checks establish
different things; a successful run is not universal proof.

The editor loads optional checks from the backend's shared SDK catalog and shows
each entry's summary and its four-part contract: the files you must produce, what
the host recomputes for itself, what a pass proves and what it does not prove.
The last list is never empty — a check that claims a pass proves everything is
rejected at load time. Selected data checks still prescribe profile/rules/audit
stages and file formats. Changing an instruction does not remove those
constraints or edit checker semantics. The SDK supplies the selected
contract to the agent, so users do not need to reproduce a JSON schema in prose.
The [roadmap](../sdk-migration.md) tracks reducing unnecessary prescription while
preserving useful independent checks. General task descriptions remain available
without selecting any domain check.

See [runtime rules](../runtime.md) for permissions, approvals, artifact limits,
correction and recovery. Current validation is indexed [here](../evidence/README.md).
