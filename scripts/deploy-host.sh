#!/bin/sh
# Compatibility entrypoint: one native build and launcher on Linux and macOS.
# Run under the operator's chosen service manager for unattended operation.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
if [ -f .env.deploy ]; then
 set -a
 . ./.env.deploy
 set +a
fi
make setup
exec ./scripts/run-local.sh
