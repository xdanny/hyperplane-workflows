# Architecture

Hyperplane lets a person describe useful work, review how it will run, and inspect
what happened. A workflow combines instructions, permitted tools, optional checks
and approval points. The agent remains general-purpose: a domain name changes the
instructions and available checks, not the model runtime.

## Responsibility boundaries

| Component | Owns |
|---|---|
| Codex SDK | Model conversations, tool use and SDK events |
| SDK extension in `packages/agents` | Run a turn, apply an optional verifier, return findings for bounded correction |
| Go application | Run lifetime, permissions, durable checkpoints, saved definitions and approvals |
| PostgreSQL | Workflow revisions, run/session state, decisions and event journal |
| Managed run folders | Produced files and logs, with inventories and hashes |
| Web and CLI | Authoring, explicit launch, inspection and cancellation |

`workflowCodex` applies the application's configuration isolation.
`runChecked` accepts an actual SDK thread, verification and persistence callbacks.
It does not implement another model transport or tool loop. The Go bridge waits
for committed startup and acknowledges each checkpoint only after persistence.
A failed checkpoint or cancellation stops execution.

## Where the SDK runs

The backend currently has two languages: Go serves HTTP and owns storage/run
lifetimes; a Node process executes the official SDK. `internal/sdkruntime` owns
SDK run startup, checkpoints, approvals, output verification and workspace locks.
It has no dependency on the retired Go graph engine. `internal/codex` launches
the server-side, reusable SDK extension in `packages/agents` over a private
worker protocol. The React browser calls HTTP and receives results; it does not load the SDK, hold its credentials
or execute workflows. The package lives separately for direct SDK reuse, not
because it belongs to the frontend. The SDK owns workflow declaration validation.
Go invokes its bounded, read-only validator; the browser obtains the result through the backend. Application request
decoding, saved revision checks and execution permissions remain application duties.

SDK launches store the actual SDK request and settings, without a synthetic graph
specification. Editor and run DAG views project dependency links from that request
and execution states from SDK checkpoints. Node cards and inspectors expose the
actual instructions, declared inputs, ancestor context, permissions and recorded
outputs; expected work remains separate from verification evidence. Legacy graph inspection
endpoints are removed; SDK results do not resolve catalog or filesystem graphs
with matching names.

## Current execution paths

**Node workflows** use `runNodeWorkflow`. The same saved definition is edited
through drag-and-drop, click-to-add, per-step descriptions, or a reviewed proposal
from an overall goal. Optional `dependsOn` links define the SDK DAG; omitted links
preserve the previous-step sequence. Independent read-only steps run at most two
at a time; tasks and approvals run alone. Explicit dependency workflows use fresh
conversations with ancestor documents as context. Legacy sequential documents
share a read-only writing conversation. Research
and independent review use separate read-only conversations. General work uses a
fresh writable step folder and a separate SDK thread. Extra acceptance checks are
optional for these managed steps. Output presence and integrity remain required;
completion without an extra check does not claim independent correctness.
Approvals pause in the application without making a model call.

**Project tasks** use `runTask` against an explicit existing folder. An extra
caller-supplied acceptance command is optional in the editor, API and CLI. Without
one, completion does not establish independent correctness. Backlog processing
runs tasks in order and still requires an acceptance command per item.
The app ideator delegates to `runTask`, supplying only fixed instructions and a
verifier. These entrypoints share conversation, permissions, correction and
checkpoint handling.

**Board planning** uses one read-only SDK conversation to turn an explicit
description or a completed run's saved SDK evidence into general-purpose tickets.
It imposes no platform scaffold or specialist agent roles. The request owns
planning cancellation; a project, lane and complete ticket plan commit together.
Creating the plan does not start workers.

**Board execution** starts one ready ticket as a managed SDK project task. The
run and ticket claim commit together under the existing ownership and workspace
locks. Completion moves the ticket to review; a person marks it done before
dependent work becomes ready. Edits pin the displayed run and ticket version so
an old review cannot approve a newer attempt. Automatic worker pools and model
review loops are removed.

**Evaluation and optimization** use the read-only Codex SDK bridge when Codex is
selected, without implicit checkout logs. Local candidates, judges and proposals
call the OpenAI Go SDK directly without agent tools.
Optimization quotes feedback explicitly associated with the skill; only configured
evaluation cases provide scores. Evaluation errors fail the comparison. Routing evaluates the
actual skill instructions and atomically saves both case snapshots and its
resulting policy while those definitions remain current.

**The interactive console** is an SDK conversation with application tools for
creating projects and saving or updating workflows. These tools reuse the same
backend handlers, SDK validation and revision checks as the editor. A private
bridge exists only while its terminal session is connected. Filesystem access
remains read-only; ambient host integrations are disabled. Saving a workflow
does not run it. Conversation history is not a durable delivery record. The
separate Shell terminal runs trusted host commands.

**ML engineering** is an ordinary seven-step SDK template: plan, review, approval,
baseline/evaluation task, review, model card and review. Its optional prediction
check observes sandbox execution and recomputes submitted prediction metrics with
trusted code. It does not prove label custody, split correctness or that the
training agent never accessed evaluation data.

**Legacy graph execution is retired.** Named and inline graph launches, process
registration, graph repair, graph inspection, node optimization and the old
scheduler packages are removed, along with residual connector executors and the
static `/api/connectors` catalog. SDK tasks use explicit access settings; stored
connection records are preserved. All launch surfaces use SDK workflows. Run and
artifact storage still retains some older field names; they do not provide an
alternate execution path. Delivery summaries now read the frozen SDK request and
checkpoint instead of graph proof trees or repair annotations.

## Templates and checks

Templates supply editable instructions, owner inputs and review criteria. A user
can create general task, document, research, review and approval steps without
choosing a domain template. The goal proposer makes one SDK call, then validates the proposed
steps or expands a supported checked-work recipe. Applying, saving and running
are separate actions. Every shipped template prepares the same SDK definition;
process registration and graph drafts are not part of template preparation.

A saved workflow can declare the facts that change from run to run as inputs, the
same slot shape templates use. Instructions reference them as `{slot:id}` tokens; the
server fills them at launch with the reviewed values, validates the filled definition
and records the values on the run. Preparing a template keeps its inputs instead of
baking values into prose, so a saved template becomes a reusable workflow. Every run
launched from a saved workflow keeps that lineage; run rows and the workflow history
view use it for the run number, duration, failing step and comparison. This is
parameterization of a frozen definition, not a scheduler: nothing starts a run except
a person, the API or the CLI.

`packages/agents/checks.json` supplies check descriptions, input schemas, table fields and allowed values, ordered
prerequisites and approval requirements. The Node worker reads it, the Go backend
embeds it, and `GET /api/workflow-checks` exposes it for the editor. Generic runners
validate and dispatch these declarations without branching on domain names.
Trusted SDK adapters still implement each optional check; catalog text alone
cannot introduce executable code.

The node editor requests validation for its current draft through
`POST /api/workflows/validate`. The same SDK validator checks saved definitions,
template preparation, proposals and run admission. It checks step ordering, access
declarations and check input relationships without starting an agent or reading
user source files. Execution preflight separately checks source availability.
Catalog metadata supplies input labels, choices and table editing guidance; the
browser no longer owns domain-specific validation. Delayed validation cannot
authorize a newer draft, and validation service failures require a successful retry
before saving or running.

Selected data checks remain prescriptive: their descriptions include methodology
and file contracts, visible in the UI. Instructions are editable, but selecting a
check binds its current requirements. Arbitrary checker semantics are not editable
workflow text. Reducing unnecessary prescription while retaining useful independent
measurement remains roadmap work. A model's ability to perform work does not remove
the value of recomputing a metric or checking file integrity. The data-quality
check now permits one complete executable rule and does not demand an invented
blocker severity. Its negative fixture may target any approved executable rule.
The host observes execution and checks reported counts and identity; those
model-authored fixture outcomes remain reported evidence, not independent causal
proof that the rule detected the seeded input. The pipeline check accepts one
direct output model or a dependency-ordered set of models. The plan explicitly
names its output; no staging/layer taxonomy is required. Source coverage, exact
approval, output identity, grain and measure checks still apply.

Keep three conclusions distinct: a structural check verifies its declared rules;
a separate model review adds a fallible judgment; a human approval records a
person's decision about the presented evidence. None proves unrestricted correctness.

Delivery summaries count explicit optional acceptance checks and independent model
reviews from SDK checkpoints. File creation and human approvals add no check count;
unchecked completion remains zero. The UI says “checks passed”, which describes
the recorded result without proving factual correctness. Continuations appear as
new attempts in the same delivery, linked to their source. Their server-owned
launch record pins the delivery root even if an earlier source is later deleted.
A human outcome is immutable for the reviewed current attempt, never inherited by
later work. Submissions name that run; stale attempts and duplicate decisions are
rejected. A continuation can receive its own decision without replacing the old one.
Historical graph proof and repair histories are not reconstructed.

Optional skill extraction and review read each source run's SDK checkpoint.
Automatic learning considers only the accepted current attempt, and proposed
evidence quotes must occur in the excerpt shown to the model. Draft promotion
remains a separate human decision. Explicit [knowledge export](docs/knowledge-layer.md)
writes the saved SDK request, checkpoint and human outcome without changing Git
state; it does not copy output files or establish new verification.

## Persistence, access and effects

Saved workflows use revision checks. A launch freezes its reviewed settings;
subsequent edits do not change that run. Every application workflow launch records
an SDK runtime fingerprint. The Go bridge compares the installed package with
its embedded source identity before starting the worker; the worker checks the
requested identity before executing any workflow. Checkpoints, approval requests
and final results carry the same versioned identity, which the bridge validates.
This covers the explicit runtime source inventory, check catalog and dependency
manifests. It does not fingerprint installed dependencies or host binaries, freeze
the execution environment, or preserve an SDK conversation for exact resumption.

Produced files and completed step documents carry paths, sizes and SHA-256 hashes.
The SDK checks them before later steps, after approval and at completion. The Go
backend also checks completed documents before presenting an approval and before
publishing a successful result. File inspection detects observed changes; it does
not provide filesystem immutability.

Writes are limited through the SDK permission configuration. Explicit connected
steps can name hosts, read paths and environment references. Ordinary read-only
SDK execution is not a minimal filesystem read allowlist. Caller-supplied checks
and shell commands execute as trusted host programs outside the SDK sandbox.
Never treat a working directory or model instruction as an authorization boundary.

Each current run holds a PostgreSQL session lock and writes checkpoints on that
same connection. A disconnected owner cannot publish a late success through a
pooled connection. Cancellation and abandoned-run reconciliation commit the
interrupted snapshot, delivery, decision dismissal and terminal event together.
The worker aborts when its backend control channel closes. Startup preserves live
session owners and interrupts abandoned work without replaying it.

Deliberate continuation of an interrupted workflow with omitted dependency links
creates a successor run. Explicit dependency workflows currently require a fresh
run; continuation rejects them before preparing a workspace.
A read-only review checks the runtime, frozen definition and completed-file identity;
confirmation rechecks them and claims one successor. Only pinned nonapproval work
from the completed prefix is copied to its new folder. The unfinished step starts
with a fresh SDK conversation, and every completed approval is requested again.
The original receipt and partial outputs remain available. The original token
budget and committed usage carry forward; an interrupted turn may have unreported
usage. A workflow interrupted after all steps completed can finish using that work,
still renewing its approvals.

Continuation blocks an already-begun unfinished task that declares a custom check,
access, toolchain installation or side effects. There is no external-effect
reconciliation or automatic replay. Matching installed dependencies, restoring an
execution environment and exact SDK thread resumption remain outside this contract.
Historical runs are not migrated into resumable checkpoints.
See [runtime guarantees](docs/runtime.md) and the [remaining work](docs/sdk-migration.md).
