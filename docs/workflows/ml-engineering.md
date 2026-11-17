# ML engineering

The ML template uses seven ordinary, editable SDK steps: plan the baseline,
independently review the plan, approve it, build and evaluate, review the evidence,
write a model card, and review the card. It uses the same task, review, approval and
document runtime as other workflows. No specialist ML agent orchestration is needed.

## Prepare and run

After `make seed`, choose **ML engineering** from the workflow templates. Supply
the business task, prediction target and label availability, actual data sources,
train/evaluation split policy, and success metric table. Its first row is primary;
fix the comparison direction and threshold before seeing results. Constraints,
runtime, environment and handover are optional owner facts.

The primary metric check supports binary labels 0 and 1 with AUROC, PR-AUC
(average precision), F1 or accuracy. F1 and accuracy additionally require a decision
rule: a threshold, top count or top percentage. Other metrics can be secondary
results requiring separate review; the attached check does not independently
validate them.

Review and edit the prepared steps before saving or running. Source descriptions
and environment notes grant no permissions. Configure any required access and
tool setup separately; unavailable sources must remain explicit gaps. Approve the
reviewed plan before training. Prepare handover documents locally without sending
or deploying anything.

The same template is available through the existing template API and CLI:

```sh
hyperplane template show ml-engineering
```

Use the displayed fields with `template instantiate`; preparing the SDK definition
starts no model work unless launch is requested. Process registration or promotion
is not required. The definition is in [sdk_ml_template.go](../../internal/seed/sdk_ml_template.go).

## What is checked

The evaluation task attaches the optional `prediction-metrics` check from the
shared SDK catalog. It receives the owner-declared success metric and data
description. The task writes predictions, an evaluation report and its check
program, then executes `sh checks.sh` inside the SDK sandbox after its edits.
The host observes that execution; it does not execute the generated program.
Trusted SDK code recomputes the primary metric and writes `score-report.json`.

The extra check binds the reported metric, threshold and comparator to the primary
owner declaration, compares the reported result with recomputed arithmetic, binds
the result to prediction-file identity, checks declared evaluation identity when
available, and requires recorded evidence of a controlled leakage check. The
catalog displays its exact artifact and report requirements. Removing the extra
check also removes these deterministic acceptance guarantees.

Editable instructions and independent model review cover the broader methodology:
meaningful naive predictors on the same evaluation rows, train-only preprocessing,
the declared split, feature and label timing, executed leakage controls, uncertainty,
worst slices, resource/privacy limits and reproducible code. Model review is fallible;
these requirements are not all independently established by metric arithmetic.

## Reusable metric tool and limits

`scorePredictions({predictions,declaration,signal})` also works directly through
`@hyperplane/agents/ml-scoring`. It reads an absolute predictions CSV containing
`y_true,y_score` and optional `slice`. Non-finite scores, unsupported labels and
malformed files fail. Slice decisions use the global selection; top-count ties
use CSV order and percentage selection rounds half to even. Unscorable slices
remain visible.

The scorer snapshots and hashes the input and computes using disk-backed SQLite.
It requires Python 3 with standard-library SQLite, accepts predictions up to 2 GB
and bounds fields, lines, slices and reports. Each scratch file is capped at 8 GB;
there is no aggregate scratch quota. Cancellation stops the worker and removes
its private scratch directory. See [the scorer](../../packages/agents/src/workflows/ml-scoring.mjs).

Recomputation verifies the metric of submitted rows. It does not prove correct
labels, complete evaluation coverage, absence of leakage or training independence.
A recorded hash identifies bytes; it does not prove the training agent could not
read the holdout. Training/holdout custody is not yet an enforced and proven access
boundary. The plan, evidence review and model card must state that limitation.
Local training and a passing score do not establish production readiness or
authorize deployment. Current verification is indexed in the
[validation records](../evidence/README.md).
