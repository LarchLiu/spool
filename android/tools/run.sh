#!/bin/sh
# Keep all disposable tool state inside an owned run, not the system temp folder.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"
export GOCACHE="$ROOT/build/cache/go-build"
export GRADLE_USER_HOME="$ROOT/build/cache/gradle"
mkdir -p "$GOCACHE" "$ROOT/build/tmp"
RUN_ROOT=$(mktemp -d "$ROOT/build/tmp/android.XXXXXX")
cleanup() { rm -rf -- "$RUN_ROOT"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$RUN_ROOT/tmp" "$RUN_ROOT/go"
export TMPDIR="$RUN_ROOT/tmp" TMP="$RUN_ROOT/tmp" TEMP="$RUN_ROOT/tmp"
export GOTMPDIR="$RUN_ROOT/go"
"$@"
