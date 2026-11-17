# Optional knowledge features

Knowledge catalogs, project declarations, stored memory and exports are optional
application features. They are not prerequisites for a workflow and do not create
another agent runtime. Normal SDK execution isolates ambient skills and memory.

## Find and inspect

The application exposes unified search at
`GET /api/kb?q=<text>&kinds=skill,radar&limit=20`. The CLI supports
`hyperplane kb search <query>`. Stored memory can be inspected and managed through
`/api/kb/memory` and the `hyperplane kb memory` commands. Saving an item does not
automatically insert it into every SDK prompt.

`make seed` imports optional local catalogs and editable workflow templates.
Templates are starting definitions for review; model execution still uses the
shared SDK worker. See [workflow templates](workflows/README.md).

## Project facts and execution context

Project declarations record reusable source references, definitions, environment
constraints and delivery preferences. Template fields can be prefilled from them.
Review inherited values for the current run: a stored source path is not proof
of current contents or permission to access it. Credentials remain environment
references, never values embedded in declarations.

The graph-era `contextKeys` and `@run.context.<key>` injection path is retired.
SDK runs retain their actual request and checkpoint. Project facts and optional
knowledge are supplied through explicit application inputs; storing a memory item
does not grant it access to SDK conversations.

## Propose and review reusable knowledge

An accepted, completed SDK run can propose a draft skill. Extraction reads only
that run's saved request and completed checkpoint, with bounded excerpts. It
requires any configured checks to have passed. Ordinary unchecked work can still
produce a draft, with the lack of independent verification stated explicitly.
Pinned quotes must occur in the checkpoint excerpt shown to the extraction model;
instructions alone, invented quotations and other runs cannot supply evidence.

Drafts remain proposals. Deduplication, human approval and the learned-skill
evaluation requirement still apply. Reviewers read the same source-run SDK
records and distinguish program checks, fallible model reviews and completion.
Deleting a source makes its evidence unavailable; descendants cannot replace it.

Optional automatic learning considers the current accepted, completed attempt
once. A later accepted attempt gets its own claim; an older success cannot
substitute for failed or unaccepted current work. Learning remains off by default.

## Export deliberately

```sh
bin/hyperplane knowledge save <runId> --target /absolute/knowledge-repository
```

This is an explicit write to the selected destination. Ordinary successful runs
and approvals do not automatically write or commit knowledge into project targets.
The target directory must exist. Export writes `README.md` and `snapshot.json`
under `knowledge/runs/<run-id-slug>-<id-hash>/`. The snapshot contains the finished
run's saved SDK request, checkpoint, continuation lineage and applicable human outcome. It does not copy
or reverify produced files or external sources. Recorded inventories remain
references, and human acceptance remains separate from check results.

An identical export is idempotent. Different existing contents cause an error and
are preserved; use another target directory to keep both snapshots. Writes stay
within the selected target, and the CLI reports write failures with a nonzero
exit. Export does not stage files, commit, run Git hooks or push. Git publication
is a separate action owned by the caller.

The old `run_context` reader and writer are removed. Unified search supports
skills, memory, decisions and technology radar. Requests for the retired
`run_context` kind or its tag filter return 400. Applied database migrations are
retained, but the old context table is no longer an application knowledge source.

Automatic learning and model-written outcome briefs are off by default; enabling
them permits extra model calls through their configured application paths.

Current isolation and persistence rules are in [runtime.md](runtime.md).
