#!/bin/sh
# The SDK owns conversation state and model execution. The Shell tab remains
# available for explicitly requested host commands and workflow CLI operations.
set -eu
ROOT="${HYPERPLANE_PLATFORM_ROOT:-$PWD}"
cd "$ROOT"
exec node "$ROOT/packages/agents/src/console.mjs"
