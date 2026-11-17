# Hyperplane

Hyperplane is a local application for giving coding agents work you can inspect. Start with a project brief, split it into tickets, and run the next ready ticket against a workspace. The Board saves the result for a person to review before dependent tickets can start. The same application has a workflow editor for repeatable jobs with inputs, files and acceptance checks.

## From a ticket to a decision

Suppose a project needs a CSV reader and a report that uses it. On the Board, make the report depend on the reader. When you start work, the Go backend selects a ready ticket, claims it in PostgreSQL and starts a worker through the Codex SDK. The worker receives the ticket title and implementation details. A successful run puts the ticket in review. A failed run leaves it blocked. Accepting the reader makes the report eligible for a later run.

The split is deliberate. The SDK manages the model conversation and tools. Hyperplane owns which ticket was assigned, which workspace it can change and which human decision allows the next task to proceed. The React client reads this state from the API. [Ticket selection](internal/store/sdk_ticket.go) · [Worker handoff](internal/sdkruntime/ticket.go)

## What a check can establish

The [checked-file example](examples/sdk-checked-work/README.md) starts with four synthetic expense rows. An agent writes a totals file in its writable directory. A separate Node program compares the input with the original and checks that the file contains exactly two totals, food 15.00 and travel 6.00. If the check fails, this workflow allows one correction. You can read the entire [acceptance test](examples/sdk-checked-work/acceptance.mjs) before running it.

That example uses a trusted host program outside the writable output directory. For a direct project task, the caller can likewise supply a check and enable one correction. Board tickets follow a different path: the runner disables automatic repair and hands successful work to a person for review.

## Start locally

Install Go 1.26, Node 22+, Docker with Compose, and authenticate Codex. Then:

```sh
make setup       # start PostgreSQL, install dependencies, build API and web app
make up          # serve the app at http://127.0.0.1:4252
```

Linux and macOS run the API and Codex natively; Docker runs PostgreSQL. Ctrl-C
stops the API. `make down` stops the database without removing its volume.
`make seed` installs optional templates and catalogs. See [runtime setup](docs/runtime.md#setup-and-configuration).

## Create a workflow

1. Open **Workflows → Build with nodes**. Add steps by dragging, clicking, or
   entering one description per line. **Suggest steps from goal** proposes an
   editable definition for your review.
2. Choose what each step does: write a document, research live sources, review
   previous work, wait for your approval, or perform checked work that creates files.
3. Review inputs and permissions, then save the definition or run it. Saving
   never starts work. Open the run to inspect progress, files, checks and decisions.

Declare the facts that change between runs as inputs rather than editing the
instructions each time: give each one an id, a hint written for whoever fills it
in, and an example, then reference it as `{slot:id}` in any instruction. Saving
keeps the tokens, so the definition you reviewed is the one that runs again;
`bin/hyperplane workflow run <name> --input k=v` supplies the values at launch,
and a missing required input is refused with that input named. Five shipped
templates are built this way for weekly and monthly work — see
[workflows you rerun every period](docs/workflows/README.md#workflows-you-rerun-every-period).

Templates are optional starting points. They use the same editor and SDK sequence;
a market-research template does not install a different kind of agent. Some data
templates currently require built-in file contracts and checks. Their remaining
special cases are tracked in the [SDK roadmap](docs/sdk-migration.md).

Steps run in order. Branching, automatic crash recovery and general external-app
actions are not implemented. Every shipped template, including ML, uses SDK steps.
See [workflow guidance](docs/workflows/README.md) for the precise current capabilities.

## Work on an existing project

Choose **Project task**, describe the change and select an existing folder. You
can supply a trusted acceptance program; without one, completion does not claim
independent correctness. The model edits that folder; the host runs any supplied
check. Optional correction reuses the same conversation after a failed check.

```sh
bin/hyperplane task --target /absolute/project \
  --check '["node","/absolute/checks/acceptance.mjs"]' \
  --auto-repair --json "Fix keyboard activation"
```

Acceptance commands run with host privileges. Keep their programs and dependencies
outside the editable folder when the worker must not change them. See
[project-task rules](docs/runtime.md#direct-sdk-project-tasks).

The [SDK package](packages/agents/README.md) also works directly with an ordinary
Codex SDK thread and application-owned verification and persistence callbacks.
The [checked-file example](examples/sdk-checked-work/README.md) and
[backlog example](examples/sdk-backlog/README.md) show the existing integrations.

## Verify changes

```sh
make sdk-install
make test-unit              # no database connections
make test-db                # separate disposable PostgreSQL at port 15497
make gate                   # SDK tests, formatting, vet, build, Go tests, UI lint
make web-build web-unit     # frontend build and unit tests
```

Test processes use `TEST_DATABASE_URL`, create separate databases and remove only
those databases afterward. They do not use the application database.
The [verification guide](docs/evidence/README.md) lists the fixture tests and a live-model example. The [runtime guide](docs/runtime.md#verification) includes Linux checks.

## Navigate the source

| Area | Responsibility |
|---|---|
| `packages/agents` | Shared SDK execution, optional verification and tools |
| `internal/sdkruntime`, `internal/codex` | Backend SDK run lifecycle and worker transport |
| `internal/sdkworkflow`, `internal/sdkplanning` | Definition validation and reviewed proposals |
| `internal/store` | Saved definitions, approvals, run state and migrations |
| `apps/api`, `apps/cli`, `apps/web` | User interfaces |
| `internal/seed` | Editable starting templates |

[Architecture](DESIGN.md) · [Operations](docs/runtime.md) · [Remaining work](docs/sdk-migration.md)
