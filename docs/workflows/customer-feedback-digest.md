# Monthly customer feedback digest

Group one month of exported feedback into themes with counted rows and verbatim
quotes, fail the digest on paraphrased quotes, counts that cannot be reproduced
or personal data left in a quote, and write three recommendations only after a
person approves the themes.

## What you supply

| Input | Where the value comes from |
|---|---|
| `exportPath` | The absolute path of the CSV or text export your feedback tool produced, on the machine the run executes on. Export it before each run: the workflow reads a file and never connects to the tool, so the file is the whole evidence base. |
| `month` | The month whose feedback is digested, with its first and last day. Rows outside it are reported as out of scope rather than folded in. |
| `productArea` | Optional. One area to narrow to, plus how a row is recognised as belonging to it — a column value, a tag, or words to look for. |
| `handover` | Which list the digest is written for, by handle or list name. |

Quotes are copied character for character and personal data is redacted before
anything is written down: names, email addresses, phone numbers, account and
order identifiers, and company names that identify a single customer are
replaced with `[redacted]`, never with a plausible substitute. A quote that
cannot survive redaction is dropped in favour of another row.

## What the check proves, and what it does not

The review step re-reads the export and fails the run when: a theme has fewer
than two quotes found verbatim in the export; a quote differs from its row by
anything other than a `[redacted]` replacement or a marked ellipsis; a quote has
no row identifier or points at a different row; a theme's count cannot be
reproduced by counting its rows; the row totals — in window, excluded, fitting
no theme — do not reconcile with the export's row count; a quote still contains
a name, email address, phone number, account or order identifier, or a company
name identifying one customer; or a themed row falls outside the declared month
or product area. It recounts the two largest themes itself.

A pass means the quotes are real and the counts add up. It does not mean the
export represents your customers, that a loud theme is an important one, or that
silence about a problem means it is not happening — people who churn quietly
leave no rows. The redaction check is a model reading text, not a data-loss
prevention scan: treat it as a second pair of eyes, not as a guarantee, and
export without direct identifiers where you can.

The recommendation step runs only after the approval and may use only the themes
you approved. It writes a document; sending it and acting on it are your
decisions.

## Rerun it next period

```sh
bin/hyperplane workflow run customer-feedback-digest \
  --input exportPath=/absolute/data/feedback-2026-10.csv \
  --input month='October 2026, 2026-10-01 through 2026-10-31 inclusive' \
  --input productArea='the mobile app: rows whose area column is "mobile"' \
  --watch
```

`bin/hyperplane workflow runs customer-feedback-digest` lists the previous
months with their outcomes, so you can see which digests were accepted and when.
Nothing schedules this: exporting the file and running the command each month is
someone's job — see
[the repeatable-workflow example](../../examples/repeatable-workflows/README.md).

Source: [starting instructions](../../internal/seed/sdk_recurring_templates.go).
