# SDK architecture roadmap

The objective is one capable agent runtime with reusable tools, editable workflow
instructions and optional checks. The SDK owns conversations and tool execution;
Hyperplane owns permissions, persistence, human decisions and result inspection.
Current behavior is documented in [DESIGN.md](../DESIGN.md) and [runtime.md](runtime.md).

## Working today

- Agent workflows use the official Codex SDK through shared wrappers. Supporting
  local-model evaluation uses the official OpenAI SDK without agent tools.
- Workflows support drag-and-drop, click-to-add and descriptions. Goal suggestions
  require review before application; saving and launching remain explicit.
- Saved workflows declare reusable inputs (`{slot:id}` tokens filled at launch), keep
  their saved-workflow lineage on every run, and expose a history with run numbers,
  durations, failing steps and comparison. The CLI launches a saved workflow by name
  with `--input` values; scheduling itself remains unimplemented.
- Ordered documents, research, independent model review, approvals and general
  file-producing work share one SDK sequence. Managed task steps and direct SDK
  `runTask`, including API/CLI project tasks, support optional extra checks.
  Backlog items retain their explicit acceptance-command requirement.
- One SDK-owned check catalog supplies the backend and editor with descriptions,
  input fields, table constraints, prerequisites and approval requirements. Generic runners use
  catalog metadata rather than domain-specific branches.
- The backend and editor use the SDK's pure workflow validator for declarations,
  dependencies, access syntax and check input relationships. Validation does not read
  source files or start a model; save and launch enforce the same rules.
- Saved definitions use revision checks. Checkpoints, cancellation, file hashes,
  bounded reads and explicit step access are enforced and tested. Completed step
  documents carry hashes checked before approval and final publication.
- Application workflow requests, checkpoints, approval requests and results carry
  a versioned runtime fingerprint. Backend/package mismatches stop every workflow
  before execution. Identity covers runtime source, checks and dependency
  manifests; it does not cover installed dependencies, host binaries or a frozen
  environment.
- SDK run ownership lives in `internal/sdkruntime`, without a graph engine.
  Launch records retain the SDK request without synthetic DAG metadata. SDK run
  DAG views consume dependency links from that request and states from its
  checkpoints; legacy graph inspection is removed. Optional `dependsOn` links
  support two independent read-only SDK steps per wave, with tasks and approvals
  exclusive. Missing links preserve sequential behavior. Explicit dependency
  workflows use fresh conversations and ancestor prompt context; they currently
  require fresh runs instead of continuation.
- Data quality, dataset shift, reconciliation, pipeline changes, data pipelines,
  market research, travel, research, interview preparation, code review and finance
  review and ML engineering prepare editable SDK steps. ML uses ordinary task,
  review, approval and document steps, with a reusable prediction-metric check.
- Legacy graph planning/critique, public graph drafts and draft promotion,
  draft-to-process conversion, refinement, asset and fanout execution are removed.
  Templates prepare SDK definitions only; the last shipped process and its
  automatic seed/deployment path are removed.
- Public graph launch/catalog/repair/inspection and process registration are
  removed, together with the scheduler, DAG, process and preflight packages,
  graph schema, demonstrations and graph-only CLI/UI features. SDK logs, files,
  checkpoint telemetry and accepted-run skill proposals remain available. Orphan
  connector executors and the misleading static `/api/connectors` catalog are
  removed; SDK access settings and existing stored connection records remain.
- SDK ownership now uses a PostgreSQL session lock and same-connection checkpoints.
  Control-channel loss cancels SDK work; cancellation and abandoned-run recovery
  preserve a complete interrupted receipt. Startup does not replay work.
- Interrupted ordered workflows support deliberate continuation into one successor
  run after read-only review and explicit confirmation. Runtime, definition and
  pinned-file identity are rechecked; completed nonapproval work is copied and all
  completed approvals are renewed. The unfinished step starts fresh. Original
  receipts and partial outputs remain; the original budget and committed usage
  carry forward. Stale reviews cannot launch and same-token retries are idempotent.
  Already-begun unfinished tasks with custom checks, access, toolchain installation
  or side effects are blocked pending effect reconciliation.
- Delivery and project summaries use frozen SDK definitions and checkpoints.
  Check counts exclude ordinary completion and approval decisions. Continuations
  remain visible as attempts with a pinned delivery root; previous human outcomes
  do not cover later work. New attempts can receive new immutable outcomes;
  submissions name the reviewed run and reject stale or duplicate decisions.
  Graph proof/repair projections and unused browser
  derivation are removed. Historical graph runs are not reconstructed.
- The optional data-quality check accepts one complete executable rule without
  demanding an invented blocker. Negative fixtures can target any approved
  executable rule; their model-authored outcomes remain reported evidence, not
  independent causal proof.
- The optional pipeline check accepts direct source-to-output builds and multiple
  dependent models without mandatory staging layers. The approved output and
  model definitions remain pinned, with source coverage and measurement checks.
- Review charters contain current editable criteria. Historical audit ledgers and
  personal infrastructure notes are archived outside the repository. Startup no
  longer imports a personal decision log over saved decisions.
- Skill extraction and review read source-run SDK checkpoints without descendant
  graph evidence. Learning considers the accepted current attempt, and draft
  quotations must occur in its displayed checkpoint. Explicit knowledge export
  writes the saved SDK receipt with error reporting and no automatic Git changes.
  Unused graph context storage readers and writers are removed. Unused run
  handover stamping and its brief projection are removed; declared handover
  requirements still prefill editable workflow instructions.
- Board planning reads completed SDK requests and checkpoints or an explicit
  description. It no longer reads graph factory outputs or forces iOS/Swift
  scaffolding. Planning errors reach callers; each lane and its complete ticket
  set commit atomically before the API or CLI reports success.
- Board execution claims one ready ticket through the managed SDK task runtime,
  sharing cancellation, ownership and workspace locks. Completion requires human
  review before dependencies become ready. Stale edits cannot replace a newer
  attempt. The separate worker pool and automatic model reviewer are removed.
- Auxiliary SDK conversations are read-only and no longer scatter logs into the
  checkout. Skill optimization uses explicitly associated SDK feedback as context,
  without manufacturing evaluation cases. Evaluation and routing preserve actual
  instructions and case snapshots, fail on execution or judge errors, and save
  routing evidence and policy atomically.
- CLI watching confirms the saved terminal state; a stream ending or a completion
  notification alone cannot report success.

## Remaining work, in order

1. **Simplify optional check contracts.** The generic agent already handles
   ordinary work without a domain check. Selected catalog checks still combine
   prescriptive methodology, file contracts and deterministic validation. Make
   those requirements understandable and reduce unnecessary prescription; extract
   reusable measurement tools with real consumers. Do not claim that their
   semantics are freely editable merely because descriptions are visible. Keep
   independent checks where they establish a useful fact, and avoid rebuilding
   domain-specific agent runtimes or duplicating catalog rules across languages.
   Workflow declaration rules and cross-table input constraints now have one SDK
   implementation, used by the backend and editor. Keep input guidance aligned
   with that implementation as optional checks evolve.
2. **Prove ML data and model custody.** The template and metric validation now use
   the SDK path. Establish a real training/holdout read boundary and frozen-model
   provenance before claiming evaluation data was unseen or predictions came from
   the approved model. Scoring submitted labels and predictions cannot prove
   either. Preserve source coverage and uncertainty instead of upgrading a hash or
   reported leakage-control result into a custody guarantee.
3. **Keep read models aligned with the SDK.** Delivery and project summaries now
   use SDK requests and checkpoints, including continuation lineage and explicit
   check results; skill extraction and review use the same per-run evidence basis.
   Remaining storage labels can be simplified without changing
   current approval, outcome or knowledge contracts. Applied migrations remain
   intact; historical graph reconstruction and migration into continuation
   checkpoints are not required. Do not turn check counts or human acceptance into
   unrestricted correctness claims.
4. **Extend recovery only with explicit guarantees.** Reviewed continuation now
   covers interrupted workflows with omitted dependency links, including renewed approvals and finishing
   already-completed work. It starts a successor with fresh conversations; it does
   not resume an exact SDK thread or support direct project-task/app-ideator
   continuation. Installed dependencies and execution environments are not frozen.
   Define runtime availability and environment restoration, and establish effect
   reconciliation before allowing interrupted connected or host-command tasks to
   repeat. Committed usage carries forward but may omit an interrupted turn's
   unreported consumption. Keep these limits visible instead of automatically
   restarting work.
5. **Connect useful external tools.** Add explicit, reviewable integrations through
   the shared SDK/tool path. Destination allowlists alone do not establish remote
   write authorization. No general external-app action system is currently shipped.
6. **Validate the whole user journey.** A new user must be able to describe work,
   edit steps, configure required access, run, inspect evidence and resolve a
   decision. A real API/database browser journey now covers per-node descriptions,
   reviewed overall-goal proposals, palette drag, click-to-add, drag reordering,
   saving and executing the same definition. A second journey configures a file
   source and credential reference, rejects missing setup before model execution,
   and verifies the resulting file. Extend this to real external tools and wider
   device/input coverage. Keep
   necessary permissions and checks understandable without exposing runtime jargon.

## Completion criteria

The migration is complete only when shipped workflows use the shared SDK path,
unused graph infrastructure is removed, user-facing setup works end to end, and
restart/access/effect behavior has an explicit tested contract. Passing one recipe
or one operating-system gate is progress toward that result.

Use [validation records](evidence/README.md) for test and live-run evidence. Keep
historical investigation and experiment output out of this roadmap; update the
current state when implementation changes instead of appending another chronology.
