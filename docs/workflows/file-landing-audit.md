# Monthly file landing audit

Measure the file that is supposed to land each period, fail the audit when the
row count falls outside the range you declared or when a figure was not actually
measured, sign off, and hand over a scorecard. Nothing is sent.

## What you supply

| Input | Where the value comes from |
|---|---|
| `filePath` | The absolute path of the file, on the machine the run executes on. One file per run; concatenate a multi-part drop first, because a pattern matching nothing looks exactly like a file with no rows. |
| `expectedRows` | The row range a healthy drop falls in, as `min–max`, counting data rows and not the header. Set it from the last few periods: the check fails outside it. |
| `period` | The period you expect the file to cover. The audit compares the dates inside the file against it, so a stale drop shows up as a finding rather than as a healthy row count. |
| `keyColumns` | The columns that should together identify one row. With no natural key, write `none` first and then the closest candidates (`none, order_id, source_system`); the audit reports whether they actually hold. |
| `handover` | The channel or list the scorecard is written for, by handle or list name. |

## What each step establishes

The profiling step is ordinary checked work: it writes `counts.json` (row count,
null and blank counts per key column, repeated key combinations, per-column date
minimum and maximum, file size and modified time) and `profile.md` (what was
measured, with the commands actually run). Completion means those files exist —
not that the numbers are right.

The review step is where the audit has teeth. It fails the run when: the
measured row count falls outside the declared range; a declared key column has
no null-and-blank count; any value is a placeholder, a TODO, a round figure with
no measurement behind it, or a number reasoned from a column name; a date column
has no minimum and maximum, or the dates found fall outside the declared period
without being reported as a finding; the file was sampled without stating the
sample size and method, or a sampled figure is presented as a full-file count;
the repeated-key count is missing or the `none` candidates were never tested; or
the file was unreadable and the profile reports counts anyway.

A pass means this drop looks like the drops you declared you expect. It does not
mean the values are correct, that the upstream job ran, or that the rows mean
what their column names say. A file can land on time, in range, and be wrong.

The approval runs **every period**, not only when the counts drift: this
workflow has no conditional steps. A clean period is a quick approval; a
drifting one is where you stop the line before the scorecard goes out.

## Reading the file: current constraint

This audit wants a read-only grant on the folder the file lands in and no
network access at all. The node validator does not accept that combination yet —
an access block must name at least one host — so the profiling step ships with
no access block and reads through the paths a task step is already given.

Until the zero-host read grant lands, one of these applies:

- keep the audited file under a path the run can already read, or
- copy the period's file into the run's own workspace before the run, and point
  `filePath` at the copy, or
- run the audit as a **Project task** against the folder, supplying your own
  acceptance program, when the file must stay where it is.

Do not add an unrelated host to the step just to make an access block parse: the
grant would be wider than the audit needs, and wider than the scorecard claims.

## Rerun it next period

```sh
bin/hyperplane workflow run file-landing-audit \
  --input filePath=/absolute/data/landing/orders-2026-10.csv \
  --input period='October 2026, 2026-10-01 through 2026-10-31 inclusive' \
  --watch
```

`expectedRows` and `keyColumns` stay saved with the workflow — they are the
declaration the check is judged against, and changing them per run to fit a bad
drop is how an audit stops being one. Raise the range deliberately, and say so.

`bin/hyperplane workflow runs file-landing-audit` shows the previous periods
with their row counts and outcomes, which is the drift history this audit exists
to produce. Hyperplane runs nothing on a schedule; see
[the repeatable-workflow example](../../examples/repeatable-workflows/README.md)
for wiring the command to a timer.

Source: [starting instructions](../../internal/seed/sdk_recurring_templates.go).
