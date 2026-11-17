# Rerunning a saved workflow every week or month

Hyperplane has no scheduler. Nothing inside it wakes up on Monday morning, and
no template installs a timer. What it has is a workflow you can save once and
launch again with new input values, and a CLI command that does the launching.
Everything below is the ordinary operating-system timer calling that command.

Five shipped templates are written for this shape — the facts that change each
period are inputs, and each one stops at a human approval:
`competitor-brief`, `kpi-commentary`, `plain-language-pass`,
`customer-feedback-digest` and `file-landing-audit`. Their guides are in
[docs/workflows](../../docs/workflows/README.md).

## The loop

Prepare the template once in the editor, review every step, and save it. Then a
run is one command:

```sh
bin/hyperplane workflow run competitor-brief \
  --input weekWindow='the last complete Monday–Sunday week before the run date' \
  --json
```

Useful neighbours:

```sh
bin/hyperplane workflow list                    # saved workflows and their ids
bin/hyperplane workflow show competitor-brief   # each input with its hint and example
bin/hyperplane workflow runs competitor-brief   # previous periods, outcomes, durations
```

`workflow run` prints the run id and exits; add `--watch` to follow it. A missing
required input exits with status 2 and prints each missing input with its hint,
so a timer that is misconfigured fails loudly instead of running with a gap.

## On a timer

A wrapper script keeps the timer honest about failure:

```sh
#!/bin/sh
# monday-brief.sh — launch one week's competitor brief.
set -eu
export PATH="/absolute/hyperplane/bin:$PATH"
# The API must already be running; this script does not start it.
curl -fsS http://127.0.0.1:4252/api/health >/dev/null || {
  echo "hyperplane API is not answering; no run launched" >&2
  exit 1
}
hyperplane workflow run competitor-brief \
  --input weekWindow='the last complete Monday–Sunday week before the run date' \
  --json >> /absolute/var/log/monday-brief.log 2>&1
```

cron, Monday at 07:00 local time:

```cron
0 7 * * 1 /absolute/bin/monday-brief.sh
```

launchd (macOS), `~/Library/LaunchAgents/local.hyperplane.monday-brief.plist`:

```xml
<plist version="1.0"><dict>
  <key>Label</key><string>local.hyperplane.monday-brief</string>
  <key>ProgramArguments</key><array><string>/absolute/bin/monday-brief.sh</string></array>
  <key>StartCalendarInterval</key><dict>
    <key>Weekday</key><integer>1</integer><key>Hour</key><integer>7</integer><key>Minute</key><integer>0</integer>
  </dict>
  <key>StandardErrorPath</key><string>/absolute/var/log/monday-brief.err</string>
</dict></plist>
```

Load it with `launchctl load -w ~/Library/LaunchAgents/local.hyperplane.monday-brief.plist`.

## What this does not do

- **It does not run the API.** The CLI talks to `127.0.0.1:4252` unless
  `HYPERPLANE_ADDR` says otherwise. If `make up` is not running, the timer fires
  and nothing launches — which is why the wrapper checks first and writes to a
  log you can look at.
- **It does not finish the work.** Every one of these workflows stops at an
  approval and waits for a person. A run launched at 07:00 Monday is a draft
  waiting in the Inbox, not a brief that went out. Nothing is sent, posted or
  published by any step, before or after the approval.
- **It does not notice a missed period.** There is no catch-up: a timer that did
  not fire, or a machine that was asleep, means that period has no run. Check
  `workflow runs <name>` rather than assuming the history is complete.
- **It does not refresh your data.** A workflow that reads an export reads the
  file as it is. Exporting the CSV before the run is a separate step in your
  script, or a separate job entirely; a stale file produces a well-checked
  commentary on last month's numbers.
- **It does not retry.** A failed run stays failed. Rerunning is another
  `workflow run`, launched by you after you have read why the first one failed.
- **It does not keep credentials.** Put no secrets in the command line or the
  wrapper; workflows reference credentials, and values are supplied through the
  credential flow.

## Related examples

[Checked files in an SDK workflow](../sdk-checked-work/README.md) shows the
acceptance-program side, for periods where you want a deterministic program to
recompute the numbers rather than a model to review them.
