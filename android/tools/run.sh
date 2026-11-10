#!/bin/sh
# Keep disposable Android state in an owned system-temp run.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"
exec sh "$ROOT/tools/run.sh" android "$@"
