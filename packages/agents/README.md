# Hyperplane SDK extension

One general Codex SDK agent runs tasks and workflow steps. Recipes supply
editable instructions and optional acceptance requirements; they do not require separate domain
agents. This private package uses `@openai/codex-sdk` **0.153.4** and its matching
executable on Node **22+**, Linux and macOS.

This is a reusable server-side extension, used by the Go backend through a Node
worker and directly by SDK consumers. Go owns application run lifetime, durable
checkpoints, approvals and workspace ownership. The browser calls the Go API;
it neither imports this SDK package nor holds its execution credentials.

From the repository root:

```sh
make sdk-install
npm test --prefix packages/agents
node packages/agents/node_modules/@openai/codex/bin/codex.js login
```

The packaged executable uses the account's existing Codex authentication. The
examples below use package imports from within this package or a consuming
workspace that resolves `@hyperplane/agents`. Model examples require that login
and make actual model calls.

## Run a task with an optional extra check

```js
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { runTask } from '@hyperplane/agents/task';

const workspace = await mkdtemp(path.join(tmpdir(), 'checked-task-'));
const result = await runTask({
  workspace,
  instruction: 'Create answer.txt containing exactly 42 followed by a newline.',
  check: [process.execPath, '-e',
    "const fs=require('node:fs'); if(fs.readFileSync('answer.txt','utf8')!=='42\\n') process.exit(1)"],
  maxCorrections: 1,
  maxTokens: 100_000,
  signal: AbortSignal.timeout(600_000),
});
console.log({ workspace, passed: result.passed, findings: result.findings });
```

Omit `check` and `acceptance` to complete ordinary work without an extra check.
The result then includes `verification: 'not_requested'`; completion does not mean
correctness was independently verified. Direct `runTask` does not require an
output inventory; managed workflow task nodes add output-presence and integrity
checks. API/CLI project tasks use the same optional-check behavior.

When configured, `runTask` executes the fixed `check` argument array on the host
after the agent turn and reports `verification: 'passed'` or `'failed'` with the
result. Explicit empty arrays, null and malformed commands are rejected by this
JavaScript API. Acceptance commands are trusted caller code:
keep their scripts and dependencies outside the model's writable directory.
Normal nonzero exits produce correction findings; launch failures, timeouts,
cancellation and output overflow stop execution. Host commands have a 60-second
timeout and a 64 KB output limit.

For application checks, pass `acceptance: {description, verify}` instead of
`check`. `verify({signal})` returns `{passed, findings}`; success requires an empty
findings array. Collect any required execution evidence through `onEvent`.
The ideator export, `@hyperplane/agents/ideator`, now supplies its instructions
and existing artifact verifier to this same `runTask` implementation.

## Run visual or described workflows

```js
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { runNodeWorkflow } from '@hyperplane/agents/nodes';

const workspace = await mkdtemp(path.join(tmpdir(), 'workflow-'));
const result = await runNodeWorkflow({
  workspace,
  instruction: 'Choose a small cycling app experiment.',
  nodes: [
    { id: 'ideas', title: 'Explore ideas', kind: 'document',
      instruction: 'Propose three small offline tools and identify assumptions.' },
    { id: 'review', title: 'Review the proposal', kind: 'check',
      instruction: 'Check that all three ideas have a concrete user, a small first experiment, and explicitly unvalidated demand.' },
  ],
  signal: AbortSignal.timeout(600_000),
});
console.log({ workspace, passed: result.passed, findings: result.findings });
```

The Workflows editor's dragged nodes, clicked templates and described steps use
this same node definition. `runNodeWorkflow` creates SDK conversations;
`runNodes(thread, options)` is the lower-level executor for callers supplying
thread/execution hooks. `validateNodes(instruction, nodes)` validates definitions.
It is also exported from `@hyperplane/agents/validation` without importing an SDK
runner. It throws a human-readable error, annotated with `nodeId` for a step issue.

```js
import { validateNodes } from '@hyperplane/agents/validation';
validateNodes('Prepare a report', [
  { id: 'draft', title: 'Draft', instruction: 'Write the requested report' },
]);
```

Nodes may include `dependsOn: string[]`. Omitted or null means the previous array
node (or no dependency for the first node); explicit `[]` means an independent
root. IDs may point forward, but missing, duplicate, self and cyclic links fail
validation. For example, two audits can depend on `ideas`, while `prd` depends on
both audit IDs. Reordering preserves explicit IDs and changes implicit links.

The SDK executes ready read-only steps in waves of at most two. Tasks and approvals
run exclusively. Explicit dependency workflows use fresh conversations with
ancestor outputs in prompts. `runNodeWorkflow` supplies these automatically;
custom `runNodes` callers must honor `authorThread(node, thread, {fresh:true})`
by returning a fresh author thread. This prompt isolation is not an OS filesystem
read allowlist. Reviews and approvals require real ancestor evidence, and check
prerequisites must follow a causal path rather than merely share an ancestor.

Checkpoints are serialized; aggregate usage counts each node once. The remaining
token allowance, including corrections, is divided across a parallel wave. The
correction-count limit stays per step, with the same between-turn token limit
described below. Failures abort and await peers and
prevent dependent work from starting. Approval verification checks ancestors;
final publication checks every node's artifacts.

The validator checks declaration structure, access syntax, prerequisites and pure
check input relationships. It does not call a model, execute a command, read user
source files or resolve credentials. Runtime `preflightCheck` separately verifies
source availability. Hyperplane's Go backend invokes this same validator with
bounded requests and matching runtime identity; HTTP clients can request a report
through `POST /api/workflows/validate`.
The HTTP model normalizes optional JSON null and empty kind/check-name strings
to omission. Direct JavaScript callers use omitted/undefined properties; explicit
empty command arrays and input maps are checked in both paths.
Use a new, empty output directory and 1–20 nodes with unique IDs matching
`[a-z][a-z0-9-]{0,63}`.

| Step kind | Execution and result |
| --- | --- |
| `document` (default) | Read-only SDK turn; the host saves a nonempty `<id>.md`. |
| `research` | Separate SDK conversation with live search; requires an observed search and a source URL. |
| `check` | Separate read-only conversation applies the step's review criteria and returns a structured verdict. Rejection stops the workflow. |
| `approval` | Caller-owned `requestApproval(state, {signal})` waits for an actual decision and returns `approved` or `rejected`. |
| `task` | Shared `runTask` execution in a fresh `<id>/` folder, with an optional caller command or named acceptance check. |

A document titled “Review” remains a document unless its kind is `check`.
Model review is fallible; a passed review is not proof of source accuracy or
business correctness. The SDK never supplies a human approval decision.

Managed task outputs must be nonempty and are inventoried before any acceptance
check, then rechecked at later
boundaries. Limits are 2 GB per file, 8 GB and 1,000 files per step, with bounded
streaming hashes and separate 1 MB JSON/Markdown limits. Each completed step
also records its Markdown document's path, byte count and SHA-256 hash. These
pins cover document, research, review, approval and task-summary records. The SDK
rechecks prior documents before later work, after approval and at completion;
the application backend checks them before offering approval and publishing a
successful result. Links and special files
are rejected. These checks detect changes; they do not lock files against other
host processes. Direct consumers own workspace lifecycle, persistence and
protection from concurrent writers.

## Templates and checks: current boundary

The SDK owns [checks.json](checks.json), a shared catalog of IDs, titles,
descriptions, input fields and table constraints, ordered prerequisites and
approval requirements.
The backend embeds that same source and exposes `GET /api/workflow-checks` for the
editor. Generic runners validate and dispatch its metadata without domain-specific
branches or ID-suffix conventions. The latest required check occurrences must
appear in the declared order, with any required approval after the current check.

Direct consumers can inspect this catalog:

```js
import { listChecks, getCheck } from '@hyperplane/agents/checks';
console.log(listChecks());
console.log(getCheck('data-quality-profile'));
```

Selected checks still attach prescriptive methodology and file contracts. Their
full requirements are visible in the editor. Editing node instructions does not
change the selected checker semantics; users may change or remove the extra
check. Trusted SDK adapters implement each available check, so editing catalog
metadata cannot introduce a new executable check. Reducing unnecessary
prescription while retaining useful independent measurement remains roadmap work.
Do not combine `acceptance` and `check`; inputs belong to the selected check's
schema. See [the runtime contract](../../docs/runtime.md) for exact requirements
and [src/workflows](src/workflows) for implementations.

General task proposals need only a title and instructions. Application review,
permissions and check selection remain separate: model-generated instructions
cannot grant access or supply arbitrary check commands.

The existing built-in checks observe the agent running its generated program
inside the SDK sandbox, then inspect artifacts and recompute report consistency
or arithmetic. They do not run generated programs on the host. Successful
execution and consistent JSON cannot establish every source or program claim.
Independent review and appropriate source controls still matter.

## Permissions, cancellation and checkpoints

Tasks default to workspace writes with network and web search disabled.
Document/review turns are read-only. `workflowCodex` isolates ambient host rules,
configuration, skills, hooks, memory and integrations through the pinned launcher;
authentication remains available. Advanced callers can explicitly override client
configuration and are responsible for the resulting permissions.

Tasks accept `access: {hosts, readPaths?, environment?}` for explicit connections:
1–32 exact lowercase hosts, specific absolute read paths, and names of existing
`WORKFLOW_*` environment variables. Keep credential values out of definitions.
Both `toolchain:true` and `sideEffect:true`, plus declared access and host `uv`,
are required for temporary tool installation. Temporary environments are removed
after the step. Network destination restrictions do not make a remote account
read-only; its service permissions must enforce that.

`signal`, `onEvent`, `checkpoint`, `maxCorrections` and `maxTokens` flow through
the shared runtime. Corrections default to zero and reuse the task conversation.
Checkpoint callbacks are awaited; persistence errors and cancellation stop further
work. In the application bridge, each checkpoint waits for the database commit
acknowledgement. Direct callers must implement their own durable storage.

`maxTokens` counts reported input plus output and stops subsequent turns; cached
input is already included in input. An in-flight turn can exceed the limit.
For node workflows, corrections apply per step and usage is aggregated across
steps. Standalone callers must choose deadlines and budgets. Reviewed continuation
can preserve committed work and renew approvals in a new run, as described below.
There is no automatic crash replay or external-effect deduplication. Enable
correction only when repeating the work is safe.

For an existing SDK thread, `@hyperplane/agents` exports `workflowCodex`, `Codex`
and `runChecked(thread, instruction, {verify, ...options})`. The SDK owns the
conversation and tool loop. `runChecked` adds application verification, bounded
correction and awaited checkpoints; it is not another graph scheduler.

## Application runtime identity

The Go application and its worker share the versioned SHA-256 identity computed
from [runtime-files.json](runtime-files.json). The inventory covers runtime
sources, the isolation launcher, check catalog and dependency manifests. The
backend rejects a different installed package before worker startup, and the
worker rejects a mismatched requested identity before executing node workflows,
project tasks or app ideator. Checkpoints, approval requests and final results
carry the fingerprint and version; the Go bridge validates each before accepting
it. This applies even when no optional acceptance check is requested.

Direct consumers can inspect the package identity through
`runtimeFingerprint()` and `runtimeFingerprintVersion` from
`@hyperplane/agents/runtime`. Direct library calls do not automatically apply the
application worker's identity policy or durable storage.

This fingerprints source and manifests, not installed dependency bytes, host
binaries or the full execution environment. It does not lock the package against
later changes. Continuation checks this identity together with the frozen definition
and completed-file hashes; it does not restore an execution environment or resume
an exact SDK thread. There is no legacy Go graph executor or connector operation
registry behind this package; explicit task access controls remain the connection
boundary.

## Deliberate continuation

Explicit dependency arrays are currently ineligible for continuation. Start a
fresh workflow instead. Omitted or null links retain the ordered contract below.

`@hyperplane/agents/continuation` exports `definitionFingerprint`,
`inspectContinuation` and `prepareContinuation`. Inspection validates an interrupted
node checkpoint against the current runtime, frozen instructions/nodes and execution
settings, then checks its completed documents and task files. It reads source
evidence without copying it or starting a model. Preparation copies only the pinned
nonapproval work from the completed prefix into a separate empty folder and checks
the copied evidence before returning a prepared checkpoint.
Copied task files retain their observed owner execute bit so saved scripts remain
usable. Copies have no group, other-user or special permissions; this does not add
permission-mode pinning to the saved content inventory.

`runNodeWorkflow` accepts `continuation: {checkpoint}` for that prepared folder.
It checks the checkpoint and files before any model turn and requires a fresh SDK
conversation. Reused work is skipped; every completed approval becomes pending
again. The unfinished step starts fresh. A plan with `restart:null` finishes with
completed work and any renewed approvals, without new model turns. A continuation
interrupted during renewed approval can itself be reviewed for continuation.

The original token budget, committed usage and attempts carry forward. An
interrupted turn may have unreported usage, so recorded consumption is a lower
bound. An already-begun unfinished task with a custom check command, access,
toolchain installation or side effects is blocked; the package does not reconcile
external effects. Missing or changed runtime, definition or pinned evidence also
blocks reuse. Historical checkpoints are not migrated.

The application wraps these helpers with a read-only HTTP review and a token-only
confirmation that claims one successor run. Same-token retries return that run;
stale reviews require fresh manual confirmation. The source receipt and partial
outputs remain available, and its approval decisions are never reused as permission.
Direct consumers must provide their own durable storage, concurrency control,
workspace ownership and renewed approval handling. These helpers do not supply
exact SDK thread resumption, dependency restoration or automatic replay. See the
[application continuation contract](../../docs/runtime.md#continue-an-interrupted-workflow).

## ML template and prediction check

The application's ML template uses ordinary task, review, approval and document
steps. It attaches `acceptance: 'prediction-metrics'` to the evaluation task, with
owner-supplied `inputs: {successMetric, dataDescription}`. There is no separate ML
agent runtime or graph process. Removing the check permits ordinary work but also
removes its deterministic metric acceptance guarantees.

The task produces predictions.csv, eval-report.json and checks.sh, then runs
exactly `sh checks.sh` inside the SDK sandbox after its edits. The host observes
successful execution, recomputes the primary metric, checks report agreement and
the declared threshold/comparator, and binds output identity. The trusted checker
writes reserved score-report.json only after acceptance. It never executes the
model-written check program on the host.

The report explicitly retains unverified holdout custody and model provenance.
Recomputed labels and scores do not prove an honest split, independent labels,
complete evaluation coverage or that the predictions came from the approved
model. Leakage-control fields remain reported evidence for independent review.
See [the editable template and limits](../../docs/workflows/ml-engineering.md).

## Reusable metric scoring

`@hyperplane/agents/ml-scoring` is a deterministic tool, not an ML agent:

```js
import { realpath } from 'node:fs/promises';
import { scorePredictions } from '@hyperplane/agents/ml-scoring';

const report = await scorePredictions({
  predictions: await realpath('/absolute/path/to/predictions.csv'),
  declaration: { metric: 'accuracy', operatingPoint: 'threshold 0.5' },
  signal: AbortSignal.timeout(120_000),
});
console.log(report.value, report.predictionsSha256);
```

It requires Python 3 with standard-library SQLite and scores
`y_true,y_score[,slice]` CSV with fixed binary labels: 1 is positive.
Supported families are AUROC, PR-AUC (average precision), accuracy and F1.
Accuracy/F1 require a threshold, top count or top percentage. Strict `>` stays
strict; top-selection ties use CSV order, percentages round half-even, and slice
results retain global decisions. Undefined slices remain visible with a reason.
Nonfinite numbers, unsupported labels and changing prediction files fail.

Input is limited to 2 GB, 64 KiB physical lines/fields, and 512 slice names of at
most 512 UTF-8 bytes each. Sorting uses disk-backed SQLite. Each scratch file is
capped at 8 GB, but there is **no aggregate scratch quota**. Cancellation kills the
private scorer and removes its scratch directory. The report binds submitted
predictions to a SHA256; it does not prove holdout custody, absence of leakage or
model quality.

Runnable model examples are [ideator.mjs](examples/ideator.mjs) and
[nodes.mjs](examples/nodes.mjs). Their receipts are demonstrations, not production
recovery queues. Focused package tests use explicit fixtures where model behavior
is simulated; consult [the migration tracker](../../docs/sdk-migration.md) for
remaining application work. The [validation index](../../docs/evidence/README.md)
lists the available checks.
