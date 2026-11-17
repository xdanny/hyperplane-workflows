# Monday competitor brief

Find what a named set of competitors changed inside one declared week, write a
brief of at most 400 words covering three changes, check every claim against the
page it came from, and stop for your approval before the email text is written.
Nothing is sent: the last step produces text, and a person sends it.

## What you supply

| Input | Where the value comes from |
|---|---|
| `competitors` | The list you want watched, named as their own sites name them. The list is the whole scope — an unnamed competitor is not researched. |
| `weekWindow` | The window a change must fall inside. Write it as a rule ("the last complete Monday–Sunday week before the run date") so the saved workflow still means the right week next month. |
| `audience` | Who reads it on Monday, what they already know and what they decide. "Why it matters" is written for them. |
| `handover` | Which list or channel the email is addressed to, by handle or list name. Left blank, the brief is addressed to nobody in particular. |

The research step searches live sources; it does not read your CRM, your
analytics or any competitor's internal information.

## What the check proves, and what it does not

The review step reopens every cited page and fails the run when: a change has no
source URL or cites one the research step never found; a page does not say what
the brief says it says; the body runs over 400 words; a change dated outside the
declared week is presented as this week's news; there are not exactly three
changes; a suggested reaction is written as a decision already taken; or a
competitor with no change found has been dropped from the brief.

A pass means every sentence traces to a source that exists and says so. It does
not mean the change matters to you, that the three chosen changes are the three
most important ones, or that a competitor did nothing — only that nothing was
published where the search looked. Model review is a separate model judgment,
not a program check: it can miss a misread page.

## Rerun it next period

Prepare the template once from **Workflows → Start from a workflow template**,
review the steps and save it. A saved workflow keeps its `{slot:…}` tokens, so
the changing facts stay inputs. Then each Monday:

```sh
bin/hyperplane workflow run competitor-brief \
  --input competitors='Toggl, Harvest, Clockify' \
  --input weekWindow='the last complete Monday–Sunday week before the run date' \
  --input audience='the four people on go-to-market; they know our product, the competitors barely' \
  --watch
```

`bin/hyperplane workflow show competitor-brief` prints every input with its hint
and example. A missing required input exits with status 2 and names what is
missing rather than running with a gap. Hyperplane has no scheduler: something
outside it has to run that command each week, and the API must be running when
it does — see [the repeatable-workflow example](../../examples/repeatable-workflows/README.md).

The run stops at the approval step and waits. Nothing after it happens until a
person decides, however long that takes.

Source: [starting instructions](../../internal/seed/sdk_recurring_templates.go).
