# Dataset shift audit

Compare a baseline window with a current window using feature-specific statistics
and tolerances. A measured difference is useful only when the two inputs are
comparable and the tolerance has a declared meaning.

Supply `baselineData`, `currentData`, `features`, `keyColumns` and `safeUse`.
The feature table records the feature, type, statistic, tolerance and unit, with
business impact/severity/ownership fields as needed. Optional `seededControl`
sets feature-specific control sizes; the contract applies its defined defaults
when none are supplied. Runtime, environment and handover context are optional.
Review all table values before running; units are not interchangeable.

Each comparable feature needs its selected statistic with the unchanged owner
tolerance and unit. A numeric or categorical feature can use PSI alone. Extra
category-emergence, volume-drift or hazard rules are optional proposals with
justified thresholds, reviewed in the same rule-set approval. Period volumes and
comparability hazards still belong in the profile; schema-incompatible features
remain explicitly unexpressible. Every approved rule must execute and account
for its checked and excluded rows. Fixed synthetic controls still use the
selected statistic and owner tolerance, with independent host recomputation.

The ten SDK steps measure both windows, review comparability, propose rules,
review tolerances, wait for approval, execute the comparison, review results,
review owner impact, explain findings and review the final explanation.
The built-in contract checks feature coverage, shared measurement definitions,
approved tolerances and controlled distortions that must be detected.

A controlled synthetic shift tests the detector; it does not replace either real
window or prove the measured change caused a business outcome. Model review must
inspect the actual programs and source evidence rather than accepting a report's
claims alone.

Prepare and edit it through the [shared workflow editor](README.md#create-review-and-run).
No process registration or promotion is needed. Source access and temporary tools
remain explicit settings on each execution step.

Sources: [template](../../internal/seed/sdk_dataset_shift_template.go),
[inputs](../../packages/agents/src/workflows/shift-inputs.mjs),
[acceptance](../../packages/agents/src/workflows/dataset-shift.mjs).
