#!/bin/sh
# Read available devices/AVDs without touching an existing adb server or persistent Android data.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "${SPOOL_ANDROID_PROBE_RUN:-}" != 1 ]; then
    exec sh "$ROOT/android/tools/run.sh" env SPOOL_ANDROID_PROBE_RUN=1 sh "$ROOT/android/tools/probe-devices.sh"
fi
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
ADB="$SDK/platform-tools/adb"
DEFAULT_PORT=${ANDROID_ADB_SERVER_PORT:-5037}
if lsof -a -c adb -iTCP:"$DEFAULT_PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
    # A second adb server may not see USB devices already claimed by the existing server.
    PORT=$DEFAULT_PORT
    OWN_SERVER=0
else
    PORT=$(bun -e 'const server = Bun.listen({hostname:"127.0.0.1",port:0,socket:{data(){}}}); console.log(server.port); server.stop(true);')
    OWN_SERVER=1
fi
export ADB_SERVER_SOCKET="tcp:127.0.0.1:$PORT"
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-$HOME/.android/avd}
export ANDROID_USER_HOME="$TMPDIR/android-home" ANDROID_EMULATOR_HOME="$TMPDIR/emulator-home"
mkdir -p "$ANDROID_USER_HOME" "$ANDROID_EMULATOR_HOME"
cleanup() { if [ "$OWN_SERVER" = 1 ]; then "$ADB" -P "$PORT" kill-server >/dev/null 2>&1 || :; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [ "$OWN_SERVER" = 1 ]; then "$ADB" -P "$PORT" start-server; fi
"$ADB" -P "$PORT" devices -l
printf '\nInstalled AVDs:\n'
"$SDK/emulator/emulator" -list-avds
