#!/bin/sh
# Foreground process; Ctrl-C stops the exact server this command started.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
command -v node >/dev/null 2>&1 || { echo 'Node 22+ is required.' >&2; exit 1; }
[ -d "$ROOT/packages/agents/node_modules/@openai/codex-sdk" ] || { echo 'Run make sdk-install first.' >&2; exit 1; }
[ -x "$ROOT/bin/api" ] && [ -f "$ROOT/apps/web/dist/index.html" ] || { echo 'Run make setup first.' >&2; exit 1; }
# On a database with no templates at all, the API installs the shipped defaults
# at startup and logs one line saying so. Set HYPERPLANE_SEED_DEFAULTS=off to
# keep an empty install empty; `make seed` refreshes templates that exist.
export HYPERPLANE_PLATFORM_ROOT="$ROOT"
export HYPERPLANE_WEB_DIR="$ROOT/apps/web/dist"
export HYPERPLANE_TARGET_ROOT="${HYPERPLANE_TARGET_ROOT:-$ROOT/workspace}"
mkdir -p "$HYPERPLANE_TARGET_ROOT"
exec "$ROOT/bin/api"
