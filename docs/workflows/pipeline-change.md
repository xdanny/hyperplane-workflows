# Pipeline change

Capture an existing pipeline's behavior, approve a proposed change and verify the
changed behavior together with the properties that must remain true. The template
works on local deliverable copies and preserves the original source.

Required inputs are `pipelineLocation`, its exact `runCommand`, `changeRequest`,
an `invariants` table, `grain`, `keyColumns` and `safeUse`. Invariants declare a
measure/behavior, value type, tolerance unit and applicable tolerance. Optional
`runtimeBudget` constrains execution in addition to the application deadline.
The command must identify the real existing end-to-end run, not a future program.

The ten SDK steps capture a measured baseline, review it, plan the change and
rollback, review the plan, wait for approval, implement and check the change,
review regression evidence, review consumer impact, explain delivery and review
the handover.

Checks compare each protected invariant with its baseline, require the requested
behavior to be false before and true after, and exercise a copied-data negative case.
Before approval, `change-plan.json` records `negativeCase.change` and a nonempty,
unique `negativeCase.expectedFailures` list of declared invariant names. Related
measures may fail together: changing a sum also changes twice that sum. The
regression report must describe the approved change and measure every invariant.
The checker recomputes verdicts from those measurements and the original owner
tolerances, then requires exactly the approved failing set, regardless of order.
Missing expected failures, additional failures and altered expectations reject
the check. All primary invariants must still pass on the real changed output.

The fixture remains agent-authored and reviewed in the plan. Recomputing report
arithmetic does not independently prove that its queries measured the actual
fixture or establish the causal correctness of the regression program.
The output must identify the actual changed files, local execution evidence and
rollback limits. Counts and successful commands cannot establish semantic
correctness without inspecting the transformation and its source inputs.

Prepare and edit it through the [shared workflow editor](README.md#create-review-and-run).
Configure source access and temporary tools per task. This template delivers
reviewable files; it does not deploy, publish a PR or modify shared infrastructure.

Sources: [template](../../internal/seed/sdk_pipeline_change_template.go),
[acceptance contract](../../packages/agents/src/workflows/pipeline-change.mjs).
