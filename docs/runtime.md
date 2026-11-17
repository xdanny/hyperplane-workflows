# Runtime guide

This guide describes the implemented runtime. The [architecture](../DESIGN.md)
explains responsibility boundaries; the [roadmap](sdk-migration.md) lists work
that is not yet complete.

## Setup and configuration

Linux and macOS run the native Go API and CLI with the pinned Codex SDK. The API
serves the built React app. Docker runs PostgreSQL only.

```sh
make setup
make up
```

Prerequisites are Go 1.26, Node 22+, Docker with Compose and Codex authentication.
The SDK uses the existing Codex ChatGPT login or API-key configuration. A new
account can use the packaged executable:

```sh
node packages/agents/node_modules/@openai/codex/bin/codex.js login
```

`make sdk-install` installs the pinned SDK/executable. `scripts/run-local.sh` is
the supported service launcher; `scripts/deploy-host.sh` builds and uses that
launcher. `make down` stops the database while retaining its volume.

| Setting | Default and purpose |
|---|---|
| `HYPERPLANE_ADDR` | `127.0.0.1:4252`, API bind address |
| `DATABASE_URL` | Application PostgreSQL connection |
| `HYPERPLANE_PLATFORM_ROOT` | Startup directory containing platform source/resources |
| `HYPERPLANE_TARGET_ROOT` | `<platform>/workspace`, default project-task target |
| `HYPERPLANE_RUNS_DIR` | `<platform>/runs`, artifacts and logs |
| `HYPERPLANE_WEB_DIR` | Built app directory; launcher configures it |
| `HYPERPLANE_TOKEN` | Bearer authentication for permitted non-loopback requests |

An exported variable, including an empty value, wins over `.env`. Keep the target
separate from platform source and run records. See [remote access](remote-access.md)
for non-loopback authentication and browser-origin configuration.

`GET /api/health` reports `ok:true` when PostgreSQL is reachable, installed SDK
runtime sources match this backend, the SDK imports and its configured executable
is available.
The SDK probe uses the same packaged launcher as execution and caches its result
for up to 30 seconds when it succeeds. A failure is retried once before it is
believed and then cached for 5 seconds only, so a cold start losing a race with
its own two-second probe does not report an unavailable SDK for half a minute. The packaged executable answers a version request; an
explicit executable override is checked for an executable file without invoking
it. That file check does not prove an override will execute successfully.
The probe makes no model call and does not check authentication,
account limits or model access; `codexDetail` states this boundary. A global Codex
installation does not satisfy a missing application SDK. The optional local model
status does not determine `ok`.

On a database whose templates table is EMPTY, startup installs the shipped
default templates and logs one line saying so; `HYPERPLANE_SEED_DEFAULTS=off`
skips it, and that decision is logged too. A database that already has templates
is left alone, because a deleted template was deleted on purpose — `make seed`
refreshes existing ones. `scripts/run-local.sh` documents the same setting.

Migrations run under a transaction-scoped database lock and apply the pending
batch atomically. Unknown migration names are refused. Existing migration contents
are not checksummed; name provenance does not establish unchanged historical SQL.
Outcome summaries use database-assigned insertion order to choose the latest
entry. Display timestamps do not determine freshness, so a backward clock
adjustment cannot select an older summary.

## SDK workflows built from nodes

All visual and text authoring paths edit one SDK node definition. A saved workflow
has `kind:"nodes"`, an overall `instruction`, and 1–20 steps with unique safe IDs.
Each step contains `id`, `title`, `instruction` and an optional `kind`:

| Kind | Execution and result |
|---|---|
| `document` (default) | Read-only SDK writing conversation; host saves Markdown |
| `research` | Separate read-only SDK conversation with live search |
| `check` | Separate read-only model review returning a structured verdict |
| `approval` | Application waits for a person's decision; no model turn |
| `task` | Separate writable SDK work folder with optional extra acceptance |

Each node can include `dependsOn: string[]`. An omitted or null value depends on
its immediately preceding array node; the first node is a root. An explicit `[]`
is an independent root. Explicit IDs may point forward in the array. Missing,
self, duplicate and cyclic dependencies are rejected by the SDK validator.
Reordering changes implicit previous-step links but preserves explicit IDs.
The editor exposes these links through **Start after** controls; goal proposals
and the console can create the same branches. Removing a referenced node requires
fixing its dependent links before saving or running.

The SDK runs ready document, research and check steps in waves of at most two.
Tasks and approvals run exclusively. A step becomes ready only after all its
prerequisites succeed. Checks and approval evidence use transitive ancestors;
check prerequisites require a causal path and ambiguous check lineage is rejected.
Approval evidence excludes unrelated siblings; final publication verifies all nodes.
Explicit dependency workflows use fresh SDK conversations and only ancestor
outputs in prompts. The ordinary read-only sandbox is not an ancestor-only
filesystem read allowlist and does not establish blinded review.

The FIRST committed record of a run already lists its declared steps, each
pending, before the SDK worker starts. A reader polling the run in that window
therefore sees the workflow that was launched rather than a run with nothing in
it. Each later checkpoint replaces the snapshot and moves the row's `updated_at`,
so per-step progress and a "last updated" reading are both real.

Parallel checkpoints are persisted in capture order and aggregate each node's
reported usage once. The remaining token allowance is divided across the wave and covers initial
turns and corrections; the correction-count limit remains per step. Token limits
are checked between turns, so in-flight turns can exceed the allowance. Failure or cancellation aborts active peers and waits for their
termination before returning. Dependent work does not start after failure.
The run graph distinguishes readiness from actual execution and marks interrupted
active steps as stopped when the overall run has ended.

`POST /api/run` accepts `{goal,nodes,project?,autoRepair?}` and returns a run ID
after startup is persisted. It rejects mixed project-task or graph settings.
This path calls `runNodeWorkflow`; it does not use the Go graph scheduler.

`internal/sdkruntime` owns backend startup, durable checkpoints, approvals and file
verification for SDK launches. It calls the worker through `internal/codex` and
has no graph-engine dependency. Run options store the actual SDK `request`, not a
synthetic graph `spec`. The run view reads this request and `doc.sdk` checkpoints;
legacy graph inspection routes are removed (404). The editor and run DAG views
project the saved dependency links and current checkpoints. Each card describes
its step type, instructions, inputs and output. Selecting a run node opens an
inspector for its full instructions, ancestor context, declared inputs, tool
access, verification and recorded evidence. The editor provides the same preview
alongside its editable fields. Expected outputs are distinguished from recorded
documents and file inventories; a pending step does not acquire an output link.
Missing search, attempt or verification evidence is not inferred. No historical
specialist roles or unavailable per-node timings are reconstructed.

The graph and inspector provide a step chooser for long workflows. Prerequisite
and dependent steps open through the same inspector, and returning to the diagram
restores focus to the selected card. Run links retain the selected step through
browser history and direct links. Inspection changes neither the saved workflow
nor its execution; editor cards still provide their separate edit action.

The run heading uses its frozen workflow name when available; a long unnamed
request uses a compact heading. The full brief and long final response remain
available in expandable sections so they do not obscure the graph.

Without explicit dependencies, document steps share a writing conversation;
previous documents provide context to later work. The host writes each `<id>.md`, with nonempty content and a 1 MB
limit. A research step must record a completed SDK search event and a source URL.
This proves a search occurred and a citation was returned, not citation accuracy.

A `check` requires an ancestor. It applies its stated criteria to prerequisite work in a
separate conversation and returns pass/fail, findings and a reader restatement.
Malformed or failed review stops later steps. When it reviews researched material,
it must itself search. Model review remains fallible; correction does not repeatedly
retry an independent review until it passes.

The reviewer starts each finding with exactly one label — `FAIL —`, `CAVEAT —` or
`CONFIRMED —` — and the worker derives `findingDetails: [{level, text}]` from those
labels, in the same order as `findings`, whose strings keep their label for every
existing reader. Levels are `fail`, `caveat` and `confirmation`; an unlabelled
finding is a caveat when the review failed and a confirmation when it passed. The
model returns no level of its own: the review schema is unchanged, and a level is
only ever read off text the reviewer wrote. `passed:true` alongside any `fail`
level is malformed and takes the existing malformed-review path, which stops later
steps. Node checkpoints and the final result carry `findingDetails` when a typed
review produced one, and omit it otherwise, so a record written before this
existed is not given levels it never had. The review document groups its findings
under "Failed", "Caveats" and "Confirmed". Counting findings is not counting
caveats: a passed review whose three findings all confirmed a criterion is
"passed, no caveats", not "passed · 3 caveats". `hyperplane show` reports each
review that way and lists the three levels separately.

A `task` can omit both `check` and `acceptance` for general file-producing work.
It must still produce files and pass the workflow's integrity checks. It has no
extra independent correctness verification. An optional `check` is a literal
executable/argument array of 1–100 strings; explicit empty/malformed arrays fail.
Alternatively choose a supported `acceptance` from the shared catalog. Do not
combine the two. Declared `inputs` belong to the selected check's schema;
unknown fields are rejected. Each task writes its own step folder. Network and
live search are off by default; explicit connections are described below.

The direct SDK `runTask` export also permits omitted extra checks and returns
`verification:"not_requested"`. With a configured check it returns `passed` or
`failed` verification. A `passed` result without a check means the SDK turn
completed; it is not independent acceptance. Managed node workflows additionally
require nonempty outputs and bounded inventories. Project tasks expose the same
optional checks through the editor, API and CLI. Backlog items still require
explicit acceptance commands.

`autoRepair:true` permits one correction per structurally checked step. Application
verification errors, transport failures and cancellation stop work rather than
becoming quality feedback. Total usage is enforced between turns; an in-flight
turn can exceed the budget. The run timeout includes approval waits.

The optional data-quality check accepts one or more complete executable rules;
one rule is sufficient. Severity follows the stated consequence, so an audit need
not invent a blocker. The seeded negative case can target an approved executable
rule of any severity. Successful execution, matching rule identity and reported
seed/detection counts are checked. These model-authored fixture outcomes do not
independently prove that the same rule caused the detection; inspect the actual
check code and fixture. Other coverage, approval and output requirements remain.

## Runtime identity

Every application workflow launch—node workflows, project tasks and app ideator—
records `request.runtimeFingerprint`. The initial checkpoint and worker states
also carry `runtimeFingerprintVersion:1`. The Go bridge compares the installed
SDK package with the source fingerprint embedded in the backend before spawning
it. The worker independently compares the requested fingerprint with its local
package before calling any workflow. A mismatch stops execution and requires a
matching backend/package installation.

The worker stamps checkpoints, approval requests and the final result with this
identity. The bridge rejects missing, changed or unsupported identity before
committing a checkpoint, processing approval or accepting completion. This also
applies to workflows with no optional check.

The SHA-256 fingerprint uses the explicit inventory in
[`runtime-files.json`](../packages/agents/runtime-files.json): runtime sources,
the isolation launcher, check catalog, package manifest and dependency lockfile.
Reads reject unsafe paths, links and oversized or changing files. Documentation,
tests, installed dependency bytes, host executables and external source data are
outside this identity. It is an installation-consistency check, not a frozen
environment or a continuous filesystem monitor. Direct SDK library callers own
any identity policy and persistence they need; the application worker protocol
provides the enforcement described here.

## Validate a workflow without running it

`POST /api/workflows/validate` accepts `{instruction,nodes}`. It returns HTTP 200
with `{valid,runtimeFingerprint,runtimeFingerprintVersion}` and, when invalid,
`issue:{nodeId?,message}`. Malformed requests return 400. An unavailable or
mismatched SDK validator returns 503; it is not reported as an input mistake.
The HTTP input model retains its omission rules: optional JSON null values and
empty kind/check-name strings are omitted before validation. Explicit empty
command arrays and input maps are preserved and checked. Direct JavaScript callers
use omitted/undefined optional properties rather than JSON null.

The backend invokes the SDK's pure `validateNodes` implementation in a short,
bounded Node process with a five-second timeout and 1 MiB request/response limits.
The runtime fingerprint must match before execution and on the response. It
validates declarations only: no model conversation, saved workflow, run record,
user source read or permission grant is created. Dataset and command descriptions
can refer to resources unavailable on the editor host; execution preflight still
checks actual availability when the work runs.

Saving, preparing templates, validating proposals and admitting runs use these
same rules. The node editor delays requests briefly while typing, ignores responses
for older drafts and keeps editing available. Its Save and Run actions require
validation of the current definition. A connection or runtime failure offers a retry.

## Approvals and saved definitions

An approval must follow output-producing work. The backend first verifies prior
completed step documents against their recorded paths, sizes and SHA-256 hashes.
Missing or altered documents prevent the approval request. PostgreSQL commits its pending
checkpoint and inbox request before the application waits. The run page and inbox
link prior documents/files for an explicit approve or reject decision. Rejection
stops later steps; approval rechecks their hashes before continuing. Repeated,
reversed or late decisions return 409. Cancellation dismisses pending requests.
Direct SDK callers must provide `requestApproval`; missing or invalid decisions
stop execution.

`GET/POST /api/workflows`, `PUT /api/workflows/{id}`, and
`DELETE /api/workflows/{id}?revision=N` manage saved settings. Updates and removals
require the last-read revision; stale writes return 409. Saving invokes no model,
creates no run and does not access a target folder. Launch uses the reviewed form
and freezes its settings. Later edits cannot change that run.

All three work types accept optional `sourceWorkflowId` and `workflowName` fields
on `POST /api/run`. The source ID records which saved setup the person edited;
execution still uses the submitted settings, including unsaved changes. It does
not claim that a saved revision ran. Changing the saved setup's revision or kind
does not replace or invalidate those submitted settings. A supplied source that
was deleted before admission produces an error. **Use as a new workflow** clears
the association while keeping the editor's current settings. A nonempty display
name must fit the existing 160-byte workflow-name limit; an empty name uses the
saved source's name or a bounded description of the submitted work.

### Reusable inputs

A saved workflow can declare the values a person supplies each run, so running it
again next week means answering its questions rather than editing prose in
several places. `inputs` is a list of the same declarations templates use
(`id`, `label`, `hint`, `required`, `example`, `type` text/list/date/number/table
with `columns`), stored on the workflow and always present, `[]` when it asks for
nothing. Ids start with a letter and use letters, digits, hyphen or underscore,
up to 64 bytes, and are unique within a workflow.

`{slot:<id>}` tokens name a declared input inside the overall goal, a step
instruction, a step's declared source input values and an `access.readPaths`
entry. A token naming an undeclared input is refused on save and on launch with
400 `{"error":…,"unknownInputs":["x"]}`. A declared input that no token
references is allowed: its value is carried into the workflow as a CONTEXT block,
exactly as template instantiation does, so a supplied value is never dropped.
Saving still calls no model and starts nothing; only the declarations and the
tokens are stored.

Because a token is not yet a value, the SDK validator treats one as a deferred
binding: a readable-input path holding a token is not required to be an absolute
path, and a check's declared source inputs are not measured for meaning. Both are
validated in full, against the actual values, when the filled definition is
admitted for a run.

Launch a saved workflow with this run's values:

```
POST /api/run {"sourceWorkflowId":"<id>","revision":<optional int>,
               "slots":{"<inputId>":"<value>"},"project":"<optional>",
               "autoRepair":<optional>}  → 202 {"runId":"…"}
```

The server reads the saved steps — the client sends no definition — fills the
tokens with the same substitution templates use, validates the filled definition
through the SDK validator and launches it with the saved workflow's lineage. A
supplied `revision` that differs from the current one returns 409
`{"error":…,"revision":N}` rather than running a version nobody looked at.
Missing required inputs return 400 `{"error":…,"missingSlots":["id"]}`. The run
records `request.slots` (the accepted values) and `projectWorkflow.sourceRevision`
(the revision that was read — provenance, not a claim that a saved revision
executed). The existing `nodes`, `task` and `app-ideator` bodies are unchanged.

The launch also freezes `request.slotLabels` — the label each value was asked
for under — so every surface can print "Week to cover: 2026-W37" rather than
`weekToCover`. Renaming an input later relabels the next run, never an old one.
Run rows and the run detail carry `slotLabels` beside `slots`.

`GET /api/workflows/{id}/runs?limit=50` returns
`{workflow, runs, total, attachable}`: the runs launched from that saved
workflow, newest first, each with its run number, start, duration, state,
values and their labels, checks declared and passed, failing step, human
outcome, committed usage, the documents its final steps produced, and `files` —
every file every step recorded (`{stepId, stepTitle, path, bytes, sha256}`,
documents included), which is what lets a comparison say that a produced data
file changed rather than only that the prose did. It is a projection over the
existing runs, usage and decision read models; there is no per-workflow run
table.

`attachable` offers the earlier runs a saved workflow could adopt:
`{runId, startedAt, state, reason}` for each run in the same project that has no
saved source of its own (`projectWorkflow.key` begins `definition:`) and whose
frozen definition hashes to the definition this workflow holds now.
`POST /api/workflows/{id}/attach-runs {"runIds":[…]}` → 200
`{attached:[…], skipped:[{runId, reason}]}` regroups them. It rewrites ONLY
`options.projectWorkflow`, to
`{key:"saved:<id>", name, sourceWorkflowId, definitionKey, attachedAt,
attachedFrom:"<old key>", version:1}`; the frozen `request` is untouched, so an
attached run still reports exactly the definition, values and checks it
executed. A run that already has a saved source, ran a different definition,
belongs to another project or predates this runtime is refused by name in
`skipped` rather than silently dropped, and each attachment journals one
`run.attached` event on its run. Because tokens are not substituted when the
workflow's own key is computed, a workflow whose instructions carry
`{slot:…}` tokens matches only runs of that same unfilled definition.

From the CLI: `hyperplane workflow attach <id|name>` lists what could be
attached, and `--run <runId>` (repeatable) attaches them.

Workflow kinds are `nodes`, `task` and `app-ideator`. Reopening a finished run's
setup creates a new run when launched; it does not resume the old SDK conversation.
Interrupted ordered workflows also offer the reviewed continuation described below.
Project tasks and app ideator use fresh setup instead.

App ideas uses the same SDK task runtime with fixed starting instructions and an
independent structural check. Launch it with
`POST /api/run {workflow:"app-ideator",inputs:{domain:"Your app brief"},project?,goal?,autoRepair?}`
or from the CLI:

```sh
bin/hyperplane ideator --json "Offline tools for weekend cyclists"
```

The ideator is absent from graph discovery. Graph-style `dag:"app-ideator"` and
`hyperplane run app-ideator` are retired and return guidance for the SDK entrypoint.

### Sign-off policy

A saved workflow declares what it asks of a person when a run finishes:
`signOff` is `required` (the default and the only previous behavior),
`optional` or `auto_when_checks_pass`. It is accepted on POST and PUT; any
other value is 400. The launch captures the policy in
`options.projectWorkflow.signOff`, so editing the workflow never restates the
terms an older run finished under, and `GET /api/runs` rows, the run detail, the
journey read models and each `GET /api/workflows/{id}/runs` history row expose
it. A row whose launch recorded no policy reads `required`, which is what that
run did — the history table therefore states each run's own terms instead of
captioning every row with the workflow's current setting.

`optional` keeps a finished delivery decidable everywhere it was before — the
run page, the workflow history, the Inbox's decided view — but it never queues:
its journey `stage` is `done` with `needsDecision:false`, and `GET /api/inbox`
outcomes and Home's "Needs you" exclude it.

The policy governs the QUEUE and nothing else. Any ended run of a delivery with
no recorded outcome — passed, failed or stopped — shows the inline Accept /
Request changes / Reject controls with a note on its own run page, whatever the
policy says; a run under `optional` adds one line saying that nothing asked for
the decision. The "Sign off in Inbox" sentence appears only where the Inbox is
actually holding the delivery, which is `required`, and `auto_when_checks_pass`
when the automatic rule declined to fire. `apps/web/src/signOff.ts` states the
two questions separately: `signOffPending` is "is the Inbox holding this?" and
`signOffDecidable` is "may a person record a verdict?".

`auto_when_checks_pass` records the acceptance itself, at terminal persist of a
PASSED run whose declared checks ALL passed (`checksDeclared` ≥ 1 and
`checksPassed == checksDeclared`), with `decidedBy: "hyperplane:auto"` and the
note "Accepted automatically: all N declared checks passed (workflow setting)".
A run with no declared checks is not auto-accepted — nothing failing is not
evidence — and neither is a failed, stopped or rehearsed run, or one that passed
only some of its checks; each of those queues exactly as `required` does. The
decision is read from the committed record, never from the worker's report, and
recording it is best-effort: if it cannot be written the run stays queued for a
person, which is the safer direction. Every surface labels it as automatic, and
skill extraction refuses it — extraction learns from a PERSON's acceptance, and
the refusal is enforced on the stored decision rather than on the caller.

Decisions carry `decidedBy`: `local` for the person at this keyboard (there is
no auth to name anyone else) and `hyperplane:auto` for the policy above.

### Reading a decision record

A decision row has two fields that look alike and are not. `status` is the ADR
lifecycle of the RECORD — every outcome is stored as `accepted`, meaning "this
decision stands" — and `decision` is the verdict a person gave (`accepted`,
`changes_requested` or `rejected`; free text for a hand-written architecture
decision). A ledger that rendered `status` showed rejections as acceptances.
`GET /api/decisions` therefore also resolves, per row, the run the decision
closed and names it: `runId`, `workflowName` and `runNumber`, projected from the
frozen launch record and the run ordinals, so no ledger has to render
`journey:j-871c…` or `run:<id>`. A decision that closed no run carries none of
the three rather than an invented name. `apps/web/src/decisionLedger.ts` owns
the row wording ("Rejected · you · 13 Sep 13:08 · Weekly topic note run #2") and
the project-summary sentence, which is derived from the same rows so it can
never read "Nothing has been accepted" above three recorded decisions.

## Continue an interrupted workflow

Continuation is an explicit new run, not an exact resumption of an SDK thread.
Workflows containing any explicit dependency array currently require **Use this
setup again** to start fresh. Review rejects them before accessing or preparing
the workspace or claiming a successor. Omitted or null links retain the ordered
continuation contract below.
On an interrupted ordered workflow, choose **Review continuation**. The backend
checks the stored request, checkpoint and source folder without starting a model,
creating a run or copying files. The review lists reusable completed steps, the
next unfinished step and every approval that must be renewed. An ineligible review
explains the blocker; it does not infer a repair or grant new permissions.

`GET /api/runs/{id}/continuation` returns `eligible`, an optional `reason`, and,
when eligible, `reviewToken`, `reused`, `restart`, `renewApprovals`,
`runtimeFingerprint` and `usageNote`. `restart:null` means **Finish with completed
work**: no unfinished model step remains, but approvals still need renewal.
An existing successor is returned as `continuedRunId`.

Confirming sends only `{reviewToken}` to `POST /api/runs/{id}/continuation`.
The backend owns the definition and reuse boundary; clients cannot submit changed
steps or a preferred restart index. A successful request returns 202 with `{runId}`.
Concurrent requests can commit only one successor. Repeating its same token returns
that successor; stale or ineligible requests return 409 and require a fresh review
and manual confirmation. After an uncertain network result, refresh the review to
check for an already-committed successor before offering another explicit attempt.
The browser never automatically posts a refreshed review. **Refresh run status**
also discovers a continuation started by another client.

Eligibility requires an interrupted SDK node workflow with a matching versioned
runtime fingerprint, definition fingerprint and reliable committed usage. The
frozen definition includes instructions, nodes, model, token budget and correction
limit. Its managed source folder must still occupy the recorded location. The SDK
verifies the completed documents and task files against pinned paths, sizes and
SHA-256 hashes. Missing or changed evidence blocks reuse. Historical runs without
these records are not migrated into eligible checkpoints.

Confirmation rechecks the source under ownership locks. Only pinned nonapproval
work in the contiguous completed prefix is copied into a separate fresh folder;
the copied files are verified before execution. Original receipts, approval
records and partial outputs remain on the source run. The unfinished step starts
with a fresh SDK conversation and folder. Every completed approval is requested
again in workflow order, including when a continuation is itself interrupted
while waiting for renewed approval. Previous decisions never authorize a new run.
Hash-verified reuse preserves the earlier evidence; it adds no new correctness
claim to a task that completed without an optional check.

The new run, first checkpoint and source's successor claim commit together. If
the database commit response is uncertain, copied evidence is retained for
recovery and no SDK worker starts on that failed admission attempt.

The original token budget, committed usage and attempt count carry forward.
Continuation does not reset the budget. An interrupted turn may have unreported
usage, so the recorded total can be a lower bound on actual consumption.
Individual run usage remains cumulative for budgeting. Aggregate usage and chart
entries count each run's new usage above its committed starting checkpoint, so
continuation does not count the same completed work twice.

If the first unfinished task already began and declares a custom acceptance
command, access, toolchain installation or side effects, continuation is blocked.
Possible external effects require reconciliation that is not implemented. Pending
steps retain their original permissions. Runtime identity covers source and
manifests, not installed dependencies, host binaries or a restored environment.
There is no automatic restart, exact thread resume or general effect replay.

## SDK step proposals and templates

`POST /api/compose {brief,project?}` returns `{workflow,unsupported,project}`.
It makes one read-only SDK planning call and returns an unsaved definition or
null. It does not launch, save, or create a graph draft. Invalid input returns
400; invalid model output or transport failure returns 502. Planning logs use a
private directory under `runs/_compose/`.

Ordinary proposals may contain task, document, research, check and approval steps.
A general task proposal contains instructions only and can create files or run
available tools in a managed folder. It cannot supply check commands, acceptance
IDs, source-input configuration, access or tool-installation grants, even as empty
fields. It cannot claim independently checked correctness without an actual check.
Checked-work suggestions currently select one of six complete templates: ML, data
quality, dataset shift, reconciliation, pipeline change or data pipeline. The
application expands its stages and criteria. Source/command values must quote
the brief exactly; unknown slots, malformed tables and missing required cells
are rejected. Model-authored acceptance commands and access grants are rejected.
Unsupported requirements are listed above the proposed steps and do not prevent
using them: the steps stay editable and the findings state what the platform
cannot do. Only a definition the SDK validator rejects cannot be applied. Valid
syntax does not establish correct business meaning or available source data;
review inputs.

`hyperplane compose --brief "…" --json` returns the proposal for explicit review,
saving and launch. Unsupported or malformed results return a nonzero exit. Its
output is not a legacy graph file for `hyperplane run`.

`make seed` refreshes named starting templates while retaining their IDs.
`POST /api/templates/{id}/instantiate` prepares an editable SDK `workflow`
without a model call, saved setup, graph draft or run. Every shipped template uses
this path; graph templates are rejected. Explicit `run:true` launches it. Required
inputs are checked; dry-run and goal overrides are rejected. Project declarations can prefill template fields; review the
resulting values for the current task. See [template guidance](workflows/README.md).

Preparation for review (the default, `run:false`) KEEPS the `{slot:x}` tokens and
returns the template's declarations as the prepared workflow's own `inputs`,
alongside the accepted `slots` values. Saving that result therefore yields a
workflow that can run again with new values instead of one with this run's
answers written into its prose. `run:true` is unchanged — it fills the values and
launches — and its run records `request.slots` like any other launch.

The proposer writes time windows relative to the run ("the last complete
Monday–Sunday week before the run date") rather than absolute dates, because a
proposal that is saved is run again later. What the application cannot do is the
application's own statement, not a model judgment: a brief asking for recurrence
(every/each Monday, daily, weekly, nightly, cron) always receives the exact
finding "Hyperplane cannot schedule runs yet — you start the workflow yourself.
Remove the schedule from your goal and press Suggest again, or run it on a timer
with `hyperplane workflow run`.", and the proposed steps are still returned so a
person can run them. Research steps use live sources by design, so their web
access is never reported as a missing capability.

## Shared optional check catalog

`GET /api/workflow-checks` exposes definitions from the backend's embedded copy of
`packages/agents/checks.json`, the same source read by the SDK worker. Entries
contain ID, title, summary, description, the four contract lists below, optional
required/optional input names, ordered prerequisites and an optional
approval-after check. The SDK validator checks the
latest preceding occurrences, so new measurements invalidate older plans and a
new plan needs a later approval. No check ID suffix determines these rules.

Every entry states its contract as four short lists of plain sentences rather than
burying it mid-paragraph: `produces` (the files the step must produce),
`recomputes` (what the host measures or verifies for itself), `proves` (what a
pass establishes) and `doesNotProve` (what it does not). Both catalog loaders —
the SDK's `check-catalog.mjs` and the backend's `checks.go` — require all four on
every entry, each holding 1–8 items of at most 200 characters, and reject an empty
`doesNotProve`: a check that claims a pass proves everything fails to load. The
`summary` is the one-paragraph intro a reader meets first (single line, at most
900 characters). `description` is unchanged and remains the prescriptive contract
the SDK hands the agent, including its resolved file paths and output fields, so
shortening it would weaken what the check actually requires.

The editor displays these requirements. Selecting a check retains its prescribed
methodology and file contracts; editing the task description does not replace its
implementation. Trusted SDK adapters implement the available checks. A catalog
entry is metadata, not permission to execute arbitrary code. General work can
choose no extra check. Extra model review and human approval remain separate steps.

## Acceptance and file integrity

Caller-supplied acceptance commands run on the host, outside the SDK sandbox.
They have a 60-second timeout and 64 KB of captured output. Only an ordinary
nonzero exit becomes correction feedback. Keep scripts and dependencies outside
the editable folder if the worker must not change them. Recording an argument
array does not freeze the files it executes.

The step's checkpoint records what the host OBSERVED in `verificationDetail`: the
command, its exit code, how long it took and up to 4 KB of its stdout and stderr,
whether it passed or failed. The document later steps read names the command and
its exit code too. That sentence exists because the agent cannot run the check —
it runs after the turn, outside the sandbox — and handoffs were therefore
reporting that the acceptance command "was not run". A recorded exit code says
the declared command ran and what it returned; it does not establish that the
command tests what its author believed.

Built-in data checks read generated artifacts and use an observer to require a
successful SDK execution of the exact `sh checks.sh` command. They do not execute
model-generated programs as trusted host acceptance. The observer pins the program
and file inventory, rejects chained/redirected substitutes, and detects subsequent
changes or failed invocations. Independent model review still evaluates program
meaning and source fidelity.

Checked outputs must contain regular files only: no symlinks, hardlinks or special
files; at most 2 GB per data file, 1,000 files and 8 GB per step, in decimal bytes.
Traversal is bounded to 4,096 entries, 32 path components and 1,024 path bytes.
Hashing streams in 256 KiB chunks with cancellation. Application-read JSON,
Markdown handoffs and individual SDK events have separate 1 MB limits.

The SDK records paths, sizes and SHA-256 hashes for task files and every completed
step document, including research, review and approval records. It rechecks them
before later steps, after approval and at completion. The backend independently
checks completed documents and task inventories before presenting approvals and
before publishing successful results. Consecutive
streamed hashes and metadata checks reject observed replacement, resizing and
same-size edits. They do not lock files or create an OS snapshot. Approval pins
produced outputs, not arbitrary external sources or the full execution environment.

The [ML template](workflows/ml-engineering.md) runs ordinary SDK task, review,
approval and document steps. Its `prediction-metrics` extra check observes the
agent executing `sh checks.sh` inside the SDK sandbox, then uses trusted SDK code
to recompute the declared primary metric from submitted predictions. It binds the
result to the declared threshold/comparator, report and file identity, and records
explicit custody limitations in its host-written score report. Generated programs
are not executed as trusted host code. Metric recomputation establishes neither
source-label correctness, population completeness, training/holdout separation nor
frozen-model provenance; reported leakage controls still need evidence review.

## Explicit SDK step access

Only `task` steps accept additional access:

```json
{
  "access": {
    "hosts": ["data.example.com", "pypi.org", "files.pythonhosted.org"],
    "readPaths": ["/data/sales.csv"],
    "environment": ["WORKFLOW_SALES_CONNECTION"]
  },
  "toolchain": true,
  "sideEffect": true
}
```

Hosts are up to 32 unique exact lowercase names without schemes, ports or
wildcards. The list may be EMPTY: reading a local file is not a reason to open
the network, so `{"readPaths":["/data/sales.csv"]}` is a valid grant and the step
runs with that read and no network at all — the permission profile disables
networking rather than standing in for it with an allowlist, and the step's
instructions say so. A grant that names no host, no readable input and no
connection name is still refused, and tool setup still requires at least one host
(installing packages needs somewhere to fetch them from).
Read paths are normalized absolute existing files/folders; a folder grants reads
throughout it, while `/` is refused. A read path holding a `{slot:x}` token is a
declared per-run value: its shape is checked once filled, before the run starts.
Optional lists have at most 32 entries.
Environment references match `WORKFLOW_[A-Z0-9_]{1,55}` and must have nonempty values
on the run host. Only names are stored or prompted. Descriptions cannot grant access.

Connected steps use the SDK's named permission profile with selected hosts, source
paths, prior outputs and a private temporary runtime. Network proxy inheritance,
local binding and UDP are disabled. Shell login profiles and unrelated environment
variables are excluded. This is stricter than ordinary workspace-write defaults;
not every workflow has a minimal filesystem read allowlist. Unsupported permission
configuration fails without falling back to unrestricted execution.

Tool setup requires `access`, both `toolchain:true` and `sideEffect:true`, and an
executable `uv` already on the host PATH. Temporary installation/cache directories
belong to the step and are removed afterward, including on failure. Instructions
require pinned versions and reproducible setup notes; packages are not permanent
host installations. Configure each execution step separately.

Hosts limit destinations, not HTTP methods, URL paths or database privileges.
Use source-side read-only credentials where appropriate. Native HTTP/S and direct
network blocking have tests; arbitrary warehouse client/protocol compatibility is
not established. Host acceptance commands retain their own privileges/environment.

All application Codex callers use `workflowCodex`: host configuration/rules, ambient
skills, hooks, memory and integrations are isolated. Existing account authentication
is retained. A specifically requested file can still be read within filesystem
permissions. Context isolation is not an additional filesystem sandbox.

## Direct SDK project tasks

`hyperplane task` and `/api/run` accept a task, an existing `targetRoot`, an optional
trusted `check` argument array, optional project scope and optional correction.
Omit `check` to request no extra check; explicit empty, blank or malformed commands
are rejected. JSON null is treated as omission by the API. Saved project tasks
return `check:null` when no check is configured and can be updated without adding
one. A task must still name its working folder.

Startup is acknowledged only after persistence. The CLI waits and returns 0 for
successful completion (including a passed check when configured), 1 for execution
or check failure and 2 for invalid arguments. Results separately report whether
verification was requested. Flags precede the task:

```sh
hyperplane task --target /absolute/project "Create a draft report from the supplied files"
hyperplane task --target /absolute/project --check '["/trusted/check-report"]' "Create the report"
```

Targets must be disjoint from platform code and run-record directories, including
through symlinks. Advisory locks reject overlapping SDK writers across cooperating
local API/CLI processes. Other host processes can still edit files. Leaving the
page does not stop the run; cancellation and application shutdown own its lifetime.

`hyperplane orchestrate --backlog file.json --target /absolute/project` and
`POST /api/orchestrate` execute selected items in order through this same path.
Each item requires a title and check; detail is optional. Progress must commit
before another item starts. Dry-run enumerates items without planning/executing.
`POST /api/orchestrate/{id}/cancel` stops the batch and its active task.

The app ideator is a fixed wrapper around shared SDK execution with its own
workspace and structural verifier. Its three concepts and comparison are documents,
not evidence of market demand. It remains available through the API/CLI and does
not require seeding a graph.

## Plan a board lane from SDK work

`hyperplane lane decompose --from-run <runId> --target <dir>` plans follow-up
work from a completed SDK run. It reads that run's frozen request and committed
checkpoint text/findings, with bounded excerpts. It does not open produced files,
reconstruct graph nodes or borrow an ancestor's output. Missing, malformed,
unfinished and rehearsal sources are rejected before planning. The new lane keeps
the source run ID and project. Recorded completion supplies context, not new
independent verification.

Alternatively, `hyperplane lane decompose --name <name> --concept <description>
--target <dir> [--project <project>]` accepts an explicit description. Optional
`--features` and `--native` lists provide requested capabilities and integrations.
Planning uses one read-only SDK conversation with general instructions; it does
not force an iOS scaffold or inject Swift shared-module requirements.

`POST /api/lanes/decompose` accepts the corresponding `fromRun` or `name` mode.
Source mode cannot be mixed with explicit description, features or project fields.
Planning belongs to the request and cancellation stops it. A valid plan contains
1–100 tickets with dependency references resolved to new ticket IDs. The project,
lane and all tickets commit in one database transaction; invalid plans or failed
inserts cannot leave a partial board lane. The response is 201 with `laneId` and
`ticketIds` only after commit; model/transport failures return an error. The CLI
waits using its model-call timeout. A lost response after commit still requires
checking the Board before retrying; this endpoint does not deduplicate retries.
Creating a plan starts no workers and does not validate the semantic quality of
model-authored acceptance criteria.

## Execute and review a Board task

The Board's **Run next task** button and `hyperplane workers --lane <id>` start
one ready ticket through the same managed SDK project-task runtime as `/api/run`.
`POST /api/lanes/{id}/work` returns 202 with the lane, ticket and SDK run IDs only
after startup commits. A task is ready when its saved dependencies exist in the
same lane and are all marked Done. Independent tasks still run one at a time in
the lane's target folder. Parallel workers, automatic model review, legacy engine
overrides and automatic retries are rejected; there is no separate worker pool.

Target checks and the normal workspace lock precede claiming the task. The SDK
owner connection atomically creates its run, first checkpoint and ticket link.
Concurrent admission rechecks the task and lane, preventing duplicate claims.
Completion commits the linked ticket and run together: passed SDK work moves to
Review, while failure or interruption moves to Blocked. Completion has no extra
independent acceptance check. A person reviews the output and marks the ticket
Done before its dependent can start. Creating or completing a ticket never starts
the next task automatically.

The ticket drawer opens its saved SDK run and cancels active work through the
normal run endpoint. API shutdown and abandoned-owner recovery interrupt the same
run and block its ticket without replaying it. Active tickets cannot be manually
edited while the SDK owner is running or stopping. A blocked task can be explicitly
returned to Todo for a new attempt after reviewing possible effects. The ticket
links its current attempt; old runs retain their ticket identity, and old completion
cannot replace a newer link or a later human decision.

Workspace locks coordinate local API/CLI writers, not arbitrary host processes.
This workflow does not provide isolated parallel checkouts or external-effect
reconciliation. A lost admission response may follow a committed claim; inspect
the Board's linked run before submitting again.

Managed task edits must copy the ticket read's `sdkRunId` and `updatedAt` values
into the PATCH request's `expectedRunId` and `expectedUpdatedAt` fields,
respectively. The Board sends these automatically. A stale or missing expectation
returns `409`, so reviewing an old attempt cannot approve a newer one or replace
its saved result. Refresh the task before deciding again.

## Interactive console

The Orchestrator terminal uses `scripts/orchestrator.sh`, retaining one SDK thread
across messages. `/new`, `/exit` and Ctrl-C are supported. Its application MCP
tools can list, create and update projects; list, save and update workflows; and
read check descriptions or validate a workflow. Requests use the same backend
handlers and revision checks as the editor. A request to create a project and
workflow can therefore produce saved records and links, rather than only a JSON
definition in the conversation. Saving does not launch work; use the workflow
editor to review and run the saved definition.

The default console receives a private loopback bridge and a random bearer token
for that terminal session. The bridge allows only those project/workflow routes,
limits request bodies and lifetimes, and closes when the terminal disconnects or
the application shuts down. It provides no execution, deletion, terminal or
arbitrary HTTP route. The official MCP SDK owns the stdio protocol; Codex owns
tool calling. The console's filesystem remains read-only, network access and
ambient host MCP integrations remain disabled. The application MCP server is the
explicit exception, with no global Codex configuration changes. Standalone console
launches without the session bridge remain conversation-only and say so.

Conversation completion is not a passed acceptance check. Reconnecting starts a
new conversation; saved projects/workflows persist, but console history is not a
database delivery. Requested persona audits belong in ordinary workflow step
instructions; simulated scores do not become customer validation.

The Shell tab and `HYPERPLANE_ORCHESTRATOR_CMD` overrides are trusted host programs.
Disconnect and shutdown stop their PTY process groups and reap the parent.
Deliberately detached host programs are outside that process-group guarantee.

## Retired graph interfaces

Named and inline graph launches, graph/process catalogs, graph validation and
preflight, graph repair, output/asset inspection and graph-node optimization are
removed. Requests to removed routes return 404; graph-shaped launch requests fail
before any work starts. The CLI exposes SDK tasks, ideator, templates and backlog
execution. Old `run`, `trigger`, `dags`, `validate`, `preflight`, `processes`,
`runs repair` and `optimize node(s)` commands are removed.

The scheduler, DAG, nested-process and preflight packages, graph schema and
demonstrations are deleted. Orphan graph connector executors and their static
`GET /api/connectors` catalog are removed; the endpoint returns 404. SDK task
connections use the explicit access settings above. Existing stored connection
records are preserved. Session logs, SDK file links, checkpoint telemetry,
knowledge, review charters and human outcome decisions remain. Graph proof-tree
and repair projections are removed; current delivery summaries use SDK evidence.
Some storage field names remain, but none can launch a graph. Historical run
reconstruction is not required.

## Workflows in project summaries

`GET /api/projects` `workflowCount` is exactly the rows `GET /api/projects/{id}`
lists: the saved workflows in the project, plus every run group the library does
not already account for — the ad-hoc ones, and any whose saved source has since
been deleted. A group launched from a saved workflow that still exists is not
counted twice. `GET /api/projects/{id}` lists a saved workflow that has
never run as a row with `runs:0`, carrying `sourceWorkflowId` so the row links
to `/workflows/<id>` without any surface parsing the group key. Saving a
workflow into a project and then reading "WORKFLOWS 0" was the project surface
contradicting the library.

Projects group runs by their saved workflow source, when one was selected.
Distinct saved workflows remain separate even when their steps are identical;
revisions and unsaved edits to one saved workflow stay in its group. Each run
still retains the exact settings it executed. Runs without a saved source group
by a backend-generated identity of their submitted definition.

`GET /api/runs` rows and the `run` object of `GET /api/runs/{id}` carry, for
codex-sdk runs, the fields that tell one run of a workflow from another:
`workflowName`, `runNumber` (1-based among runs sharing this workflow identity in
this project, oldest first), `durationMs` (null while running), `slots` (the
values this run was given), `failingStep` (`{id,title,kind}` of the declared step
whose checkpoint failed or was rejected — never the internal `delivery` node) and
`waitingForPerson` (the checkpoint is at an approval). `waitingForPerson` is
presentation only: the stored state remains `running`. The legacy `failingNode`
is unchanged for records that predate SDK steps. The whole page costs one extra
query for the run numbers and one for the failing nodes.

The backend uses one PostgreSQL projection for launch metadata, existing SDK
snapshots, overview counts and detail rows. It returns a stable `key` and display
`name` for the web and CLI. The compatibility `dag` field remains the runtime
type; clients do not use it to identify a workflow.

An ad-hoc definition includes the work type, instruction or domain, ordered
nodes with their checks and access, custom check arguments, correction setting
and an in-place project task's target folder. Generated work folders, run and
thread IDs, ownership, runtime fingerprints and environment-selected model or
budget defaults do not create another workflow group. This organizational key
is separate from the SDK's continuation fingerprint and its execution checks.

The launch captures the group identity and display name in `projectWorkflow`
metadata alongside its frozen request. A continuation carries that snapshot
forward without looking up the current saved setup. Later renaming or deletion
of the saved setup or an earlier run cannot regroup the surviving continuation.
The most recently updated run supplies the group's displayed name and run link;
run ID breaks timestamp ties deterministically. Older SDK
snapshots without a saved-source association use their frozen definition;
historical graph executions are not reconstructed.

## Delivery summaries and human outcomes

`GET /api/inbox` items carry the context their card needs, projected from the
run: an approval gains `workflowName`, `runNumber`, `goal`, `nextSteps` (the
titles of the steps that wait on it — an empty list means nothing does) and
`draftDocument` (the first document the approval pinned). A delivery awaiting a
verdict gains `workflowName`, `runNumber`, `resultDocument` (what its final step
produced) and `failureReason`, untruncated.

Delivery summaries use the frozen SDK request and committed checkpoint. Their
`criteria` and `proven` fields remain wire names for declared checks and passed
checks. Only configured task acceptance checks and independent model reviews
count. Ordinary document/file completion and human approval contribute zero.
“Checks passed” describes their recorded results; a model review remains fallible,
and a program check establishes only its stated requirements. An unchecked run
can complete and receive human acceptance without acquiring a check result.

An explicitly continued run is a new attempt in the same delivery. Its
`continuedFrom` link identifies the immediate source; the immutable launch options
also pin the delivery root independently of that source record. Deleting an
older source does not regroup the surviving continuation. The source link can
then point to a deleted run; this preserves identity, not deleted evidence.
A prior human outcome remains on record but does not cover a later attempt.
The new attempt needs its own applicable human decision as well as any renewed
workflow approvals. `POST /api/journeys/{key}/outcome` requires
`{runId,verdict,note?}`, where `runId` is the latest attempt the person reviewed.
The backend rejects a stale attempt or a duplicate decision for the same attempt
with 409. A later continued attempt can receive a new immutable decision while
preserving the previous decisions. The browser keeps the note on a failed
submission, requires a manual refresh and review, and never retries a decision
automatically.

Whether a finished delivery QUEUES for a person is the saved workflow's
sign-off policy, captured at launch (see "Sign-off policy"). `required` queues,
`optional` finishes as stage `done` without queuing, and
`auto_when_checks_pass` records an acceptance the platform labels as its own.
Journeys, inbox items and run rows carry `signOff` and, on a recorded outcome,
`decidedBy`.

Graph `proofNodes` and repair/subrepair annotations are retired from these read
models. Historical graph executions are not reconstructed. Rehearsal acceptance
refusals remain conservative guards for older records.

Global token totals and per-execution charts count only each execution's new
committed usage, subtracting the immutable continuation seed. Individual run
receipts retain cumulative usage toward the original budget. Interrupted turns
may have unreported usage in either view.

## Supervision surfaces and status vocabulary

One vocabulary describes a run on every surface. `apps/web/src/runLexicon.ts`
owns it; the Runs list, the run page, Home, the Inbox and the workflow history
all render what it returns, so a run cannot read PASSED in one place and
COMPLETED in another.

Chips are PASSED, REVIEW FAILED, CHECK FAILED, FAILED, WAITING FOR YOU, RUNNING
and STOPPED. The kind of failure comes from `failingStep.kind`: a failed
independent model review reads REVIEW FAILED, a failed program check reads CHECK
FAILED. A run parked on an approval reads WAITING FOR YOU — the stored state is
still `running`, and only the presentation changes. Acceptance is a suffix, not
a chip: `· awaiting your sign-off` until someone decides, then `· accepted by
<who> <date>` with the note quoted beneath.

A node workflow's header reads "N steps · K corrections used (1 allowed per
step)", or "N steps · no automatic correction" when repair is off. Corrections
are step attempts beyond the first. The word "attempts" is never used for a node
workflow, because it counted steps.

The run header carries `PASSED · 2 min 43 s · 428.9k in / 3.6k out · gpt-6-astra`
from `durationMs`, the checkpoint usage and `options.model`. Token figures are
truncated rather than rounded, so a displayed figure never exceeds the receipt.

A finished run leads with a "Your result" card: the documents produced by the
steps nothing else waits on (an approval or review at the end of the graph
points at what it was about), their first lines rendered, the independent
review's verdict in one line with its findings collapsed, and the next action.
"Read the review" opens the review DOCUMENT the check step recorded, in the same
reader; a check step's session log is usually empty, and opening it read as the
review having said nothing. A review that recorded no document offers its
session log instead, and the button says so. Documents open in an in-app reader
with Back and "Open raw file" rather than a bare browser tab. The boilerplate that used to sit above the results is one
"What these statuses mean" disclosure; nothing was deleted from it.

The run detail query polls every 3 s while a run is running, blocked or waiting
on a person, including while the tab is in the background, and both the approval
and the sign-off mutations invalidate the run, runs and inbox queries. The page
shows "Last updated <time>" so a lagging row is visible as a lagging row.

`GET /api/runs` is ordered by `started_at DESC, run_id DESC`. The list shows
the started column, so it is sorted by the column it shows; ordering by last
update put a run that started at 12:50 between one that started at 13:16 and one
at 12:57. Delivery and project read models continue to order by activity.

Acceptance names its decider. `decidedBy: "local"` reads as a person's
signature; `hyperplane:auto` reads as "accepted automatically · N/N checks" and
never as somebody's judgment.

A task, research or review step records WHAT it searched for, not only how
often. Its checkpoint keeps the `searches` count and gains `searchLog`:
`[{query, at, urls?}]`, at most 20 entries per step, each string clipped to 300
characters and at most 5 URLs. One SDK `web_search` item may BATCH several
queries (`queries: [...]`); every one of them becomes its own entry, sharing the
event's timestamp, and the `searches` count counts queries rather than events —
a turn that issued three queries reads as three, not one. An item whose query
this SDK does not report still counts as a search and adds no row, so nothing
renders as an empty "Search:". The reader is told once, under the list, whether
results were recorded at all, rather than per row. The boundary is deliberate — this SDK reports a
web search as the query the model issued and does not report the pages that came
back, so `urls` is written only when a runtime actually supplies result URLs,
and nothing maps a citation in a document to a search. A step that never
searched records no log at all rather than an empty one that would read as
"we looked and found nothing".

`GET /api/workflows/{id}/runs` backs the workflow history page at
`/workflows/:workflowId`: the workflow's declared inputs, a read-only step
summary, and a table of its runs with duration, inputs, checks passed of
declared, the recorded decision and who made it. Any two runs compare side by
side, including a word-level difference of their DELIVERED documents computed in
the browser — the last recorded document of a step that is neither a review nor
an approval, since a terminal review records a document of its own and diffing
it compared two reviews rather than the work. `documents[].kind` carries the
declared step kind so the browser can tell them apart. It is labelled a text difference throughout: it compares characters,
never meaning, and nothing about a comparison is stored. Beside it, an "Output
files" section compares every recorded checkpoint file of both runs by SHA-256
and reads same / changed / only in run #N. Each side of a row opens that run's
own copy of the file in the in-app reader, so a checksum is never the only thing
offered. That comparison claims only what a hash comparison can: "changed" says
the bytes differ, never what differs inside them, and a file with no recorded
checksum says it cannot be compared rather than defaulting to either answer.

The same page offers "Attach N earlier runs" when the project holds runs of this
same definition that were started before the workflow was saved. Attaching
rewrites only `options.projectWorkflow`; the frozen `request` each run actually
executed is untouched, so it changes what a run is FILED under and nothing about
what it did. Runs the server refuses come back with its reason rather than being
silently dropped.

A saved workflow carries a sign-off policy — `signOff`, one of `required`
(the default and the behaviour every workflow had before the setting existed),
`optional` or `auto_when_checks_pass`. It is chosen in the editor, where each
option states its consequence on the line below it, and it is frozen into the
launch record, so editing a workflow never rewrites what an older run asked for.
`optional` keeps a delivery decidable on the run page and in its history while
never queueing it. `auto_when_checks_pass` lets the server record the acceptance
itself when every declared check passed, and that acceptance is labelled
"accepted automatically · N/N checks" on every surface, is never attributed to a
person, and never satisfies the rule that a skill may only be drafted from a
result a person accepted. A run whose checks did not all pass — or that declared
none — is queued exactly as `required` would queue it.

One acceptance has one acceptor. The header line reads "accepted 12 Sep 2026",
naming a decider only when there is one to name, and anyone else who wrote the
note is credited for the note rather than rendered as a second person who
accepted. Run headings are a name or a plain label, never a goal sentence cut
mid-word: a saved workflow's name is rendered in full however long it is, and a
goal too long to be a title is read under "Workflow brief".

Input values are shown under the labels their workflow asked for them by:
`request.slotLabels`, frozen at launch and carried by the run list, the run page
and the history rows. A run that recorded no labels falls back to a humanized id
("weekToCover" reads "Week to cover"), never a bare identifier.

`GET /api/runs` orders by started time, which is the column every list shows.

A research, task or check step's checkpoint records the searches it ran — the
query and the top URLs it returned — and the step inspector lists them under
"Recorded execution details" as "Search: <query> → <hosts>". A recorded search
establishes what was ASKED, not that any page was read, and nothing in a
rendered document is marked as fetched on that basis.

Two capabilities that previously existed only as terminal commands are on the
surfaces that need them. A draft skill whose Approve is blocked for want of a
practice test offers "Generate practice test", which runs the same generator
`hyperplane eval gen <skillId>` runs; a 409 means one already exists and is
answered with an explicit Replace, never an automatic retry. A finished run
offers "Ask a reviewer", which records an audit persona's verdict against that
run through `POST /api/personas/{id}/review` and lists what has been recorded.
Both remain advisory: generating a practice test approves nothing, and a persona
verdict gates nothing and never replaces the evalset requirement.

Each account of a run appears once. When the result card is carrying a run's
deliverable, the run page drops the separate "Latest step result" block, and
when the card is carrying the reviewer's findings it drops the separate "Check
findings" block; a task or ideator run, or any run that recorded no document,
keeps both, because there is then no card holding that content. `resultCardVisible`
in `apps/web/src/runResult.ts` is the single rule, read by the card and by the
page around it so the two cannot disagree.

Two surfaces quote a record rather than a summary of it. `failureReason` reaches
an Inbox card already shortened to 200 characters, so "Show full reason" reads
the whole finding back from the run it links to and "Show less" restores the
clip; when the run's record carries nothing longer, the card says so and offers
the run instead of expanding to the same truncated words. A draft skill whose
pinned evidence is only a recorded step status still names it as a status, and
shows the first lines of what the run delivered underneath — labelled as the
delivery, not as evidence the extraction chose, since a document quote is not
what `pinEvidence` stores.

Remembered run-time input values are keyed by SAVED workflow id and nothing
else. An unsaved draft and a definition just prepared from a template both have
the id `""`, which is not an identity, so neither reads nor writes them;
preparing a template replaces the form's values outright rather than merging
into them, which is what let one template's answers appear under another
template's questions.

## Persistence and shutdown

The worker waits for each checkpoint acknowledgement. PostgreSQL commits session,
phase, usage, findings, delivery state and journal events before acknowledging.
Failure stops execution. Cancellation fences later writes, terminates owned process
groups and persists terminal state with a bounded independent context. API shutdown
rejects new work, cancels active runs and waits for final writes before closing
PostgreSQL. Event streaming subscribes before reading journal history.

Each new SDK run acquires a PostgreSQL session advisory lock before publishing
startup. Checkpoints use that same dedicated connection and verify its session
identity and lock. This requires direct PostgreSQL or a session-mode pooler;
transaction-mode pooling is unsupported. Each active run uses one additional
connection, separate from the application query pool.

The owner checks connection health and persisted cancellation once per second,
with a one-second query timeout. Checkpoint connection failures also cancel work
immediately. Database/network failure detection is bounded by those checks; it
is not instantaneous. The worker continuously observes its backend control pipe:
EOF, stream errors or malformed control messages abort active SDK work and approval
waits. A maximum of 1 MB is buffered for a control message, including messages
without a newline.

Startup reconciliation probes the ownership lock. Live API/CLI owners, including
owners on another host using the same database, remain running. Abandoned runs
become interrupted, retaining the last committed SDK session and evidence. The
run, delivery, pending-decision dismissal and terminal journal event commit together.
Older records without session ownership still use the local PID fallback.

Cancellation writes that same complete interrupted receipt before requesting worker
shutdown. Another process's owner observes the persisted cancellation. A late
completion cannot replace the receipt or duplicate its terminal event. Deletion
returns 409 while a run is running or still holds its ownership lock; cancel it
and wait for execution to stop first.

Startup and shutdown never replay interrupted work automatically. The explicit
[continuation review](#continue-an-interrupted-workflow) can create a successor
under its identity, evidence and effect restrictions. Approval decisions from an
interrupted attempt cannot authorize that successor. Restoring an execution
environment and reconciling external effects remain unimplemented.

Background improvement, automatic learning and model-written briefs are off by
default (`HYPERPLANE_IMPROVE_INTERVAL`, `HYPERPLANE_AUTO_LEARN`, `HYPERPLANE_BRIEFS`).
Ordinary work does not require them. Explicit knowledge export is available with
`hyperplane knowledge save <runId> --target <dir>`. It exports the finished run's
saved SDK request, checkpoint and applicable human outcome without copying output
files or changing Git state. Extraction and review use the source run's SDK
checkpoint; graph context injection and descendant proof readers are retired.
See [knowledge features](knowledge-layer.md) for the export and learning contracts.

## Running a saved workflow from the CLI

```sh
hyperplane workflow list [--project p] [--json]
hyperplane workflow show <id|name> [--json]
hyperplane workflow run  <id|name> [--input k=v ...] [--project p] [--watch] [--json]
hyperplane workflow runs <id|name> [--limit N] [--json]
hyperplane workflow attach <id|name> [--run <runId> ...] [--json]
```

`show` prints the inputs the workflow asks for with their hints and examples, its
steps, its sign-off policy stated as what will happen when a run finishes, and
its recent runs — each with the values it was given under their labels and, for
a decided run, whether a person or the sign-off policy accepted it. `attach`
with no `--run` lists the earlier runs that ran these steps without a saved
source and exits 2 with the command that attaches them; with one or more
`--run` it attaches them and reports each refusal with its reason. `run` sends the id, the revision it just read and the
supplied values; it prints the run id, and with `--watch` follows the run to its
terminal state. Missing required inputs exit 2 and print each missing input with
its label, hint and example, so the next command can be typed from the output. An
input the workflow does not declare is refused before the request is sent, and a
revision that changed while the command was reading exits nonzero rather than
running a definition nobody looked at.

Hyperplane does not schedule runs. `hyperplane workflow run` is what a cron or
launchd entry calls to repeat one on a timer; the application starts nothing by
itself.

## Watching a saved run

`hyperplane watch <runId>` reports success only when the saved run state is
`passed`. SDK `run.done` events also describe failed or interrupted work, and an
event stream can close before completion. The command reads the persisted state
on terminal events or stream closure and falls back to polling when needed.
Failed, blocked, interrupted, missing or unconfirmed runs return a nonzero exit.

## Auxiliary SDK conversations

Planning, reviewing and other auxiliary calls are read-only. The auxiliary bridge
rejects workspace-write and retired toolchain/connection grants; writable tasks
use the managed SDK workflow path. Without an explicit evidence directory, the
bridge returns its response in memory and writes no `.last.txt`, `.log` or usage
files in the caller's folder. Explicit evidence destinations require an existing
directory and a filename label; write failures propagate. Optimization and evalset
proposal calls own separate private temporary directories and remove them after
success, failure or cancellation.

## Skill feedback for optimization

`optimize --from-runs` uses only source run IDs explicitly linked in the selected
skill's metadata and preference records naming that skill. It reads each run's
own SDK checkpoint and applicable human outcome, never graph nodes or ancestor
results. Rehearsal, missing and unrelated evidence is excluded. A source association
does not prove the current skill version executed in that run.

Feedback is quoted context for the proposed rewrite. It does not create synthetic
evaluation cases or turn a successful review's caveats into praise. The configured
evalset remains the scoring contract; an evaluation outage is an error. With no
applicable feedback, explicit `--from-runs` stops before model calls and automatic
improvement skips the skill.

Applying a winner uses one conditional database update against the skill snapshot
that was evaluated. If its instruction, metadata or other saved fields changed,
or the skill was removed, optimization reports a conflict and preserves the newer
state. Two optimizers evaluating the same snapshot cannot both apply. Automatic
improvement relies on the optimizer's application receipt and does not save the
instruction a second time.

## Evaluation and routing evidence

`hyperplane eval run <evalset-id>` evaluates the stored skill named by the
evalset's `skill_ref`, or the explicit `--target skill:<id>` / inline instructions.
A missing skill or empty instruction is an error. The recorded target is the
actual evaluated instruction, not the reference label. `--engine codex` and
`--engine local` use their corresponding judge when criteria require one;
`--judge codex|local|none` chooses explicitly; `--judge-model <name>` selects its
model independently of the candidate model. Mock evaluations do not call a
judge by default and cannot certify unmeasured criteria.

Cases require unique IDs and at least one supported deterministic check or judged
criterion. Unsupported checks/options, missing check arguments, empty evalsets
and criteria without a judge fail before generation. Engine failure, judge
failure, invalid scoring output or cancellation produces a failed evaluation,
retaining partial case diagnostics with no aggregate quality score. Persistence
uses the request context: cancellation can prevent those diagnostics from being
saved, and the returned error reports that failure. Successful execution can
still fail its quality checks; that is a completed evaluation with failed cases,
not an execution error.

New result records include the case input, checks and criteria as a snapshot in
existing JSON storage. Judged cases also record the scorer provider, requested
model selection, rubric/threshold fingerprint and a local endpoint fingerprint.
Endpoint credentials are never included. A provider-default selection is recorded
explicitly; it is not a pinned model version.

Comparisons require completed results with matching case and scorer snapshots
and internally consistent scores. They refuse partial, failed or old records
lacking snapshots, and cannot compare judged cases whose scorer is unidentified.
Different candidate instructions remain comparable when evaluated with the same
cases and scorer. Use the same explicit judge configuration when comparing local
and Codex candidates; their default judges differ. Custom judges or local
endpoints whose configuration cannot be safely identified can still score cases,
but those judged results cannot justify a comparison. These records preserve
declared inputs and scoring criteria; they do not establish model quality, source provenance, or a pinned
model/runtime version when the caller used provider defaults.

Run evaluation cases with `hyperplane eval run`. The HTTP API exposes evalset and
result reads; `POST /api/evals/{id}/runs` is unsupported and returns 405. It does
not accept or queue evaluation work.

`POST /api/skills/{id}/evalset/generate {"engine"?:"codex"|"local","replace"?:bool}`
→ 201 `{evalsetId, cases}` generates a skill's practice test through the SAME
generator `hyperplane eval gen <skillId>` runs (one implementation in
`internal/optimize`). Approving a LEARNED draft is blocked until an evalset names
it, and the only remedy used to be a terminal command, which made the learning
loop unusable from the surface that asked for it. It is a billed engine call:
one skill at a time, never on a schedule, bounded at 10 minutes, and detached
from request cancellation so closing a tab does not abandon a call already paid
for. The default engine is `codex` — the platform's own engine, where the CLI
defaults to `local` for offline work. An existing evalset returns 409 with its
id unless `replace:true` says otherwise, so an edited practice test is never
overwritten by accident.

`GET /api/persona-reviews?run=<runId>` lists the advisory reviews recorded
against one run — the same question as `?target=run:<runId>`, without the caller
having to know how a target is spelled. Giving both, or an invalid run id, is
400. Persona reviews remain advisory: they are one more recorded opinion beside
the model review, never a check result. They are rendered under "Reviews
recorded", never in a run's status row, and a verdict chip reads
`Reviewer <name>: PASS (advisory)` with the reviewer's own first sentence beside
it — a bare "PASS" next to a run chip reading REVIEW FAILED read as the platform
contradicting itself, when the two judge different things over different scopes.

### What a reviewer and the skill extractor are given

Both read the same files, through `internal/rundocs`: the documents the run's
committed checkpoint recorded, under `<runsDir>/<runId>/nodes/delivery/OUTPUTS`,
which is where an SDK step writes. The checkpoint is the authority on which
files count (`doc.sdk.nodes[*].document` and `[*].files`), so nothing walks the
folder guessing, and a path that could climb out of that folder is refused. Each
document is bounded at 32 KB, the set at 128 KB and 12 documents, and a bound
that bit is stated in the evidence rather than left to be inferred.

They are captioned `<step title> · <path>`. A run review's evidence heads each
declared STEP with its own name, so a citation names the step it came from
instead of every citation reading back as one shared section heading. The
reviewer's honest-boundary note lists the documents it was given by name, or
says plainly that none could be read — the previous prompt offered neither, and
reviewers correctly reported that the deliverable was unavailable to assess
while the file sat in the run's own folder. The legacy `<runsDir>/<runId>/
artifacts` inventory is still listed, and a run whose steps wrote only SDK
documents now says so instead of reading as a run that produced nothing.

The CLI `route eval` and `POST /api/routing/eval` share one operation. Local and
Codex engines receive the same stored instruction and cases, with the same Codex
judge when criteria exist. Both comparison records and the skill-scoped policy
commit in one transaction, after checking that the skill instruction and evalset
cases have not changed. A changed definition requires another evaluation.
Execution/scoring failure leaves routing unchanged; neither a zero-pass engine
nor an empty evaluation can justify a recommendation. Confidence reports the
selected engine's observed pass rate. A policy is a stored recommendation; it
never grants workflow permissions or proves that a provider default is pinned.

## Verification

```sh
make sdk-install
make test-unit
make test-db
make gate
make web-build web-unit
```

Tests use `TEST_DATABASE_URL`, never the application connection. Each process
creates a unique database from `template0` and drops only that database. An
explicitly configured but unreachable test server fails the suite.

To verify Linux from a Mac:

```sh
docker build -f infra/test.Dockerfile -t hyperplane-tests .
docker run --rm \
  --network container:"$(docker compose -f infra/test-compose.yml ps -q db)" \
  -e TEST_DATABASE_URL=postgres://hyperplane_test:hyperplane_test@127.0.0.1:5432/hyperplane_test_admin?sslmode=disable \
  hyperplane-tests
```

Use a non-default port for browser previews and stop the owned preview afterward.
Fixture tests exercise real application/SDK plumbing with deterministic model
responses. Live model tests are separate evidence. Linux container results do not
by themselves establish physical-host sandbox parity. Available tests are listed in the [validation index](evidence/README.md).
