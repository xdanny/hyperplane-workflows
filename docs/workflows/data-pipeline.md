# Data pipeline

Build analytical tables from actual sources, with measurements that explain where
each business number came from. The agent uses the shared SDK task runtime;
built-in acceptance currently defines the required profiles, plans and reports.

Required inputs are `sources`, `targetStore`, final-row `grain`, `keyColumns`,
business `measures`, maximum source lag (`maxLag`) and `safeUse`. Each measure has
a definition, tolerance unit and allowed difference. Optional context covers
external refresh deadlines, late arrivals, runtime budget and handover.
Blank optional values remain unspecified.

The seven SDK steps are:

1. Measure sources and independent control totals.
2. Plan the analytical tables.
3. Review source evidence and the plan.
4. Wait for approval.
5. Build and verify the tables.
6. Explain the delivered tables and evidence.
7. Review the actual tables and handover.

Checks cover source identity and full row accounting, typed model dependencies,
final grain and candidate keys, each measure's independent raw-source control,
its delta/tolerance, repeated builds and a negative case that must fail. A declared
late-arrival policy needs an executed late-row scenario. Deliver only approved
finished output; keep other working tables and sensitive fields out of the final database/export.

The plan names an explicit `outputModel` and 1–32 typed models in dependency
order. Each upstream names a measured source or an earlier model; every measured
source must contribute to the output through those dependencies. The output owns
the declared grain, keys and measure mappings. A single SQL model can read sources
directly. Preparation and intermediate models are optional; this check does not
require staging, intermediate or mart layer names. The approved model definitions
and output name must survive unchanged in the build manifest.

The host observes execution and verifies the report contract and decimal
arithmetic. Row counts, source identities and negative-fixture outcomes are
reported by the authored program; structural acceptance alone does not independently
prove SQL lineage or that a reported assertion ran against its claimed data.

The requested engine must actually execute. A missing engine or inaccessible
source is a failure, not permission to substitute a fixture or claim an unperformed
build. Matching totals and successful commands still require independent review
of business logic and source fidelity.

Prepare and edit it through the [shared workflow editor](README.md#create-review-and-run).
The template delivers local files for review. It does not publish to a warehouse,
install an external schedule or send the handover to another person.

Sources: [template](../../internal/seed/sdk_data_pipeline_template.go),
[acceptance contract](../../packages/agents/src/workflows/data-pipeline.mjs).
