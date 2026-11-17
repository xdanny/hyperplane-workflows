# Dashboard reconciliation

Explain why two sources disagree at a shared row meaning and measure definition.
The result should reproduce the quantities being compared before explaining their
difference. Two totals read through the same flawed transformation are not
independent confirmation.

Supply the question, actual `sourceA` and `sourceB`, `sharedGrain`, `keyColumns`,
measure definitions for each side, tolerances and confidentiality limits. Record
known filter differences explicitly; the form also supports reporting period,
dashboard figures, runtime budget and handover context. Empty optional context
must not turn into invented business facts.

The ten SDK steps profile both sources, review measurements, propose accounting
equations, review them, wait for approval, execute reconciliation, review results,
review the remaining business question, explain findings and review the explanation.

Each measure accounts for the difference with named filters and a remaining
unexplained amount. The built-in checks validate equations, declared tolerances,
segment coverage and a controlled discrepancy that must remain unexplained.
Numeric consistency alone does not prove the filters implement the business
meaning; independent model review inspects programs and source evidence.

Prepare and edit it through the [shared workflow editor](README.md#create-review-and-run).
No process promotion is needed. The workflow produces local evidence and a report;
it does not update dashboards or authorize warehouse writes.

Sources: [template](../../internal/seed/sdk_reconciliation_template.go),
[acceptance contract](../../packages/agents/src/workflows/reconciliation.mjs).
