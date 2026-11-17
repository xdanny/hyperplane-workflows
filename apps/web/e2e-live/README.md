# Real SDK browser journey

These opt-in scenarios use Chromium against the production web build, a real Go
HTTP server and a disposable PostgreSQL database. Application endpoints are not
intercepted. The official Codex SDK runs its normal transport against a small
fixture executable that supplies deterministic proposal, document, task and review
responses, including a task that reads a real test-owned CSV. No paid model calls are required.

It describes two steps and saves a definition. It requests an overall-goal proposal,
reviews its steps, and checks that suggesting or applying them changes neither
saved workflows nor runs. After explicitly applying the proposal, it edits the
instructions, drags an approval from the palette, clicks to add a review, and
drags that review before approval. These are Chromium mouse gestures, with no
injected drag events or transfer payloads. It saves revision 2 and reloads it.
It then launches the same nodes, reads real output files, approves the pending SDK decision and records a
human delivery outcome for the viewed run. Both browser assertions and direct
store reads check persistence. The unchecked task keeps `verification:not_requested`;
only the independent review contributes to the check count.

A second journey configures a file task through Files and connections. It first
runs with a missing source file, then with a missing connection reference. Each
failure shows an actionable message, and neither starts an SDK turn. It corrects
and saves the settings, reloads revision 3, and runs that exact definition against
a test-owned CSV. The fixture receives the selected environment reference, reads
the source and writes its row count and sum. The browser verifies those output
values; the Go harness verifies stored access settings, unchanged source bytes,
connection-value absence from run/evidence records and temporary-runtime cleanup.
The fixture also asserts the real SDK arguments contain the resolved read path
and exact host allowlist, omit unrelated environment variables and contain no
connection value. The reserved `.invalid` hostname is never contacted.

From the repository root, with the web dependencies, SDK dependencies and
Playwright Chromium installed:

```sh
make test-db
TEST_DATABASE_URL='postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/hyperplane_test_admin?sslmode=disable' \
HYPERPLANE_BROWSER_JOURNEY=1 \
HYPERPLANE_BROWSER_ARTIFACTS=/tmp/hyperplane-real-browser \
DATABASE_URL= \
go test ./internal/httpapi -run '^TestSDKBrowserJourney$' -count=1 -v
```

`internal/dbtest` creates a new database from `template0` and drops it afterward.
The helper chooses fresh loopback API and preview ports, builds the web app and
owns both servers. It never uses the application's ports 4252, 5173 or 5432.
The preview and API are stopped on success or failure, and active SDK work is
drained before temporary run folders are removed. On macOS and Linux the helper
signals owned build, preview and Playwright process groups on cancellation;
Playwright closes its Chromium process during graceful shutdown, with a bounded
forced fallback for unresponsive processes in the owned group.

`HYPERPLANE_BROWSER_ARTIFACTS` is optional. When set, the folder retains the browser
receipts, SDK call ledger, preview log and screenshots of proposal review, pending
approval, missing-source feedback and connected settings. Otherwise these use a test temporary folder and are removed. Use a fresh folder for an inspectable receipt;
the fixture ledger is reset at the start of each run.

Normal Go and Linux gates explicitly skip this test unless the opt-in variable is
set; a skipped run does not establish browser coverage. The dedicated
[Playwright configuration](../playwright.live.config.ts) expects the helper-owned
server addresses and is not a standalone server launcher. The usual
[API-fixture browser suite](../e2e) remains separate.

This proves application integration and deterministic SDK transport, not model
quality, factual correctness or operating-system sandbox enforcement. The fixture
writes its task output as a local executable; physical-host permission boundaries
need their separate runtime checks. The authoring journey covers per-node descriptions,
overall-goal proposal review/application, palette drag, click-to-add, and drag
reordering. The connected journey covers a declared local read path and selected
environment reference. Neither proves physical sandbox enforcement, external
service connectivity, every node/check type, touch input or real-model quality.
The five successful SDK turns are the connected file task, proposal, document,
ordinary file task and independent review. Preflight failures, saving, approval
and delivery acceptance do not invoke the model.
