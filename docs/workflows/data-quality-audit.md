# Data quality audit

Profile actual data, propose rules from the owner's expectations, approve them,
and execute the audit. This template uses the shared SDK runtime with a built-in
acceptance contract; the model writes the programs and the application checks
specific evidence about their execution and output.

Supply the dataset, expectations, what one row represents (`grain`), identifying
columns (`keyColumns`) and confidentiality/use limits (`safeUse`). Optional runtime,
environment and handover context describe additional requirements. Use real source
references and configure needed access on each execution step.

The nine steps measure data, review the profile, propose rules, review coverage,
wait for approval, execute the approved audit, review evidence, explain findings
and review the explanation. Rules must preserve thresholds and account for every
expectation. The audit reports checked rows, violations and rates; a controlled
violation tests whether the checker can detect the intended failure.

A data violation differs from an evidence gap: missing required metadata cannot
be reported as demonstrated bad data. Successful execution and matching counts
still need review of program meaning, source coverage and the conclusion.

Prepare and edit it through the [shared workflow editor](README.md#create-review-and-run).
The original process is retired; no process promotion is needed. Local results do
not authorize remote writes or certify that every possible data defect was found.

Sources: [template](../../internal/seed/sdk_data_quality_template.go),
[acceptance contract](../../packages/agents/src/workflows/data-quality.mjs).
