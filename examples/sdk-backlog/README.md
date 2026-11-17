# Direct checked SDK backlog

After the normal Hyperplane setup and Codex sign-in, run from the repository root:

```sh
work=$(mktemp -d)
hyperplane orchestrate --backlog examples/sdk-backlog/backlog.json \
  --target "$work" --project sdk-backlog-demo --auto-repair --json
```

This example uses Node, already required by the SDK. It creates a small temperature
utility and then its documentation, each in one SDK session with at most one
correction. Every item has an explicit command argument array. The command is
executed directly, without inserting its arguments into a shell. Success means
that command exited zero; the example checks do not constitute a full product QA.

The existing Codex login is used. Each task appears as a `checked-task` run with
one SDK delivery, logs, session identity, usage and verifier findings. API callers
send the same array as `backlog` to `POST /api/orchestrate`, with `targetRoot`,
`project`, and optional `autoRepair` / `stopOnFail` / `maxItems`.

Use `--dry-run` to enumerate selected tasks without generating or executing a plan.
Old unchecked backlogs must add `check` arrays before live execution. The old
`codex-local` engine override is explicitly rejected in this path; Codex SDK uses
`HYPERPLANE_MODEL` or the documented default. Other local model paths remain
available separately.

The model's writable target must be separate from the SDK runtime and all run
records. Acceptance commands are trusted host programs. Put verifier scripts and
their dependencies outside that target if the model must not modify the checks.
Do not use this unattended example to deploy or repeat external effects.
