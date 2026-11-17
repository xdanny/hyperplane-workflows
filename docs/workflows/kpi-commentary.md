# Monthly KPI commentary with source ledger

Turn a KPI export into a ledger where every figure names the export row it came
from, add external context for the two figures that moved most, write a one-page
commentary that cites ledger rows, and hold it for your sign-off.

The ledger is the point. Every number in the commentary has a `figure_id`, and
every `figure_id` has a row in `ledger.csv` carrying its value, unit,
denominator, window, the export row it was read from, and the declared
definition as applied.

## What you supply

| Input | Where the value comes from |
|---|---|
| `month` | The reporting month with its first and last day. The run date is not the reporting period. |
| `exportPath` | The absolute path of the CSV you exported, on the machine the run executes on. This workflow reads a file; it never queries a warehouse and never refreshes anything. |
| `kpiDefinitions` | A table with a row per KPI: name, definition, unit, denominator. The definition is the one your team argues about — which rows count, which are excluded. A KPI missing from the table is not commented on. |
| `priorNote` | Optional. Last month's commentary, or the one line you want carried forward. Blank on a first run means no comparison is made rather than one being invented. |

## What each step establishes

The first step is ordinary checked work with **no extra check attached**: it
produces `ledger.csv` and `ledger.md`, and completion means the files exist and
are well formed, not that the figures are right. That is deliberate — the
arithmetic is verified by the review step, which recomputes the largest movers
from the export itself. If you have a trusted program that recomputes your KPIs,
attach it to that step as an acceptance command and you get a deterministic
check as well.

The review step fails the run when: a commentary number has no `figure_id` or
names one the ledger does not have; a ledger row lacks its source row, unit or
window; a commentary number differs from its ledger value or is silently
rounded; two figures are compared across different windows, including a
month-on-month comparison the ledger does not record; a KPI was computed
differently from its declared definition; external context is presented as a
cause where the research step recorded it as coincidence, or has no source URL;
or a "not available" figure is missing from the commentary's stated limits.

A pass means every figure traces to a row of the export and the arithmetic
holds. It does not mean the export is correct, complete or current, and it is
not an audit of the systems that produced it. If the export is wrong, this
workflow produces a well-sourced commentary on wrong numbers.

## Rerun it next period

```sh
bin/hyperplane workflow run kpi-commentary \
  --input month='October 2026, 2026-10-01 through 2026-10-31 inclusive' \
  --input exportPath=/absolute/data/kpi-2026-10.csv \
  --input priorNote='September: activation recovered to 34% after the form fix.' \
  --watch
```

`kpiDefinitions` is a table input: leave it out of the command and the saved
value is reused, which is usually what you want — the definitions are the part
that must not change between months. Export the CSV before the run; a file that
does not contain the declared month is reported as a gap, not filled in.

Source: [starting instructions](../../internal/seed/sdk_recurring_templates.go).
