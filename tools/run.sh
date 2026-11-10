#!/bin/sh
# Run a development/test command with owned, disposable system-temp storage.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec bun "$ROOT/tools/run.mjs" "$@"
