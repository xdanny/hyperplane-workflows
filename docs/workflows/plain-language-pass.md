# Plain-language pass for a technical document

Restate in one sentence what a document is for, rewrite it for a named reader,
and have a separate model conversation fail the rewrite on undefined terms,
unitless numbers, weakened caveats and buried consequences. Then approve the
wording before it replaces anything.

The review criteria are the technical editor's audit protocol in
[personas/quinn-tech-editor.md](../../personas/quinn-tech-editor.md), written
into the check step so the run carries them rather than depending on a persona
being selected. The bar is that persona's: **can a first-time reader restate
what was attempted, what happened, and what to do next?**

## What you supply

| Input | Where the value comes from |
|---|---|
| `source` | The text itself, pasted, or the absolute path of the file on the machine the run executes on. A link to a page nobody can open leaves the run with nothing to work from. |
| `audience` | Who must follow this after one read, what they already know, and what you need them to do with it. That action is what the rewrite puts where they will see it. |
| `glossary` | Optional table of `term` and `plainMeaning`. Fill it in where your team has agreed on wording; leave it empty and each term of art is simply defined at first use. |

## What the check proves, and what it does not

The review step writes its own one-sentence restatement of purpose, outcome and
next step from the rewrite alone — if it cannot, that is the controlling
finding. It then fails the run when: a term of art or abbreviation is used
before it is defined in this document; a number appears without its unit, or a
rate without the population it is out of; a caveat, limit, failed check or
uncertainty in the source is weakened, generalised or dropped; a consequence for
the reader appears after the implementation detail that causes it; the opening
does not state the outcome and the reason that controls it; the rewrite asserts
something the source does not support or drops a condition the reader must meet;
or a supplied glossary term is rendered in different words than declared.

Every finding quotes the exact passage and supplies replacement wording. A pass
means a first-time reader can restate the document; it does not mean the work
the document describes is correct, or that the source was right about it. This
workflow edits wording, and the check judges wording.

The rewrite is written into the run's documents. Nothing overwrites your file:
copying the approved text back over the source is your action, after the
approval.

## Rerun it on the next document

```sh
bin/hyperplane workflow run plain-language-pass \
  --input source=/absolute/docs/release-notes-2026-10.md \
  --input audience='support agents answering customer questions; they know the product, not the database' \
  --watch
```

The glossary is worth saving with the workflow rather than passing each time: it
is the part that should not change between documents. Pass `--input glossary=…`
only when you want to override it for one run.

There is no scheduler and no file watcher. A run happens when someone runs that
command with the API up — see
[the repeatable-workflow example](../../examples/repeatable-workflows/README.md).

Source: [starting instructions](../../internal/seed/sdk_recurring_templates.go).
