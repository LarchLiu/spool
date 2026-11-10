#!/bin/sh
# Manual, opt-in Docker verification. Never called by normal tests or releases.
set -eu
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || { echo 'Usage: sh tools/check-linux-container.sh IMAGE [native|desktop|voxl]' >&2; exit 1; }
REPO_ROOT="$(pwd)"
[ -f "$REPO_ROOT/tools/linux-compat-smoke.sh" ] || { echo 'Run from the repository root.' >&2; exit 1; }
mode="${2:-native}"
case "$mode" in native|desktop|voxl) ;; *) echo 'Unknown check mode.' >&2; exit 1 ;; esac
if [ "${SPOOL_LINUX_CONTAINER_RUN:-}" != 1 ]; then
  exec sh "$REPO_ROOT/tools/run.sh" linux-container env SPOOL_LINUX_CONTAINER_RUN=1 sh "$0" "$@"
fi
mkdir -p "$REPO_ROOT/build/cache" "$REPO_ROOT/build/artifacts/linux-compatibility"
label="$(printf '%s-%s' "$1" "$mode" | tr '/:@' '---' | tr -c '[:alnum:]._-' '_')"
lock="$REPO_ROOT/build/artifacts/linux-compatibility/.$label.lock"
mkdir "$lock" 2>/dev/null || { echo "Another check owns $lock" >&2; exit 1; }
RUN_ROOT=$SPOOL_RUN_ROOT
container=''
cleanup() {
  # Stop and wait for our container before removing the bind-mounted scratch.
  if [ -n "$container" ]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
  rmdir "$lock"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
container="spool-compat-$(basename "$RUN_ROOT" | tr '[:upper:]' '[:lower:]')"
log="$REPO_ROOT/build/artifacts/linux-compatibility/$label.log"
echo "Manually checking $1 ($mode); log: $log"
if docker run --rm --name "$container" \
  --mount "type=bind,source=$REPO_ROOT,target=/workspace,readonly" \
  --mount "type=bind,source=$RUN_ROOT,target=/tmp/spool-run" \
  --mount "type=bind,source=$REPO_ROOT/build/cache,target=/workspace/build/cache" \
  --env TMPDIR=/tmp/spool-run \
  --workdir /workspace "$1" sh tools/linux-compat-smoke.sh "$mode" >"$log" 2>&1; then
  tail -n 8 "$log"
else
  tail -n 60 "$log" >&2
  exit 1
fi
