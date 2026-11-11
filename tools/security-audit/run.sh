#!/bin/sh
# Opt-in audit probes verify the protections for the original audit findings.
# Go overlays keep these probes out of normal tests and production source files.
set -eu
REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT"
if [ "${SPOOL_SECURITY_AUDIT_RUN:-}" != 1 ]; then
  exec sh "$REPO_ROOT/tools/run.sh" communication-security env SPOOL_SECURITY_AUDIT_RUN=1 sh "$REPO_ROOT/tools/security-audit/run.sh" "$@"
fi
AUDIT_RUN_ROOT=$SPOOL_RUN_ROOT
export GOPROXY=off
export AUDIT_RUN_ROOT

case "${1:-repro}" in
  repro|android|web-ingress)
    bun - <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const root = process.cwd();
const probes = path.join(root, 'tools/security-audit/testdata');
const Replace = {};
Replace[path.join(root, 'internal/workspace/communication_security_audit_test.go')] = path.join(probes, 'workspace_audit_test.go');
Replace[path.join(root, 'android/transport/spoolnet/communication_security_audit_test.go')] = path.join(probes, 'android_audit_test.go');
fs.writeFileSync(path.join(process.env.AUDIT_RUN_ROOT, 'overlay.json'), JSON.stringify({Replace}));
JS
    if [ "${1:-repro}" = repro ]; then
      : "${CHROME_BIN:?Set CHROME_BIN to a Chrome/Chromium executable for the isolated browser probe}"
      test -x "$CHROME_BIN"
      go test -overlay "$AUDIT_RUN_ROOT/overlay.json" -count=1 -v -timeout 2m ./internal/workspace -run '^TestAudit'
    elif [ "$1" = web-ingress ]; then
      go test -overlay "$AUDIT_RUN_ROOT/overlay.json" -count=1 -v -timeout 1m ./internal/workspace -run '^TestAudit(PlaintextRemoteLogin|ProxyLoginLimit)'
    fi
    if [ "${1:-repro}" != web-ingress ]; then
      go -C android/transport test -overlay "$AUDIT_RUN_ROOT/overlay.json" -count=1 -v -timeout 1m ./spoolnet -run '^TestAudit'
    fi
    ;;
  regression)
    go test -race -timeout 4m ./internal/workspace -run '^Test(HTTPSProxy|Origin|Realtime|Relay|RemoteDesktop|Push|WebPush|AdditionalTLS|Tailnet|LocalWebURL)'
    go test -race -timeout 3m ./internal/tailnet ./internal/netproxy ./internal/appupdate
    go -C relay test -race -timeout 3m ./...
    go -C android/transport test -race -timeout 3m ./...
    go test -race -timeout 3m ./cmd/desktop -run '^Test(Chat|Settings|DesktopDev|DesktopSettings|AttachmentAccess|WindowChrome)'
    ;;
  web)
    bun run --bun --cwd web test
    ;;
  java)
    AUDIT_JAVA_HOME=${JAVA_HOME:-/Applications/Android Studio.app/Contents/jbr/Contents/Home}
    AUDIT_ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
    AUDIT_JUNIT="$REPO_ROOT/build/cache/android-java/junit-4.13.2.jar"
    AUDIT_HAMCREST="$REPO_ROOT/build/cache/android-java/hamcrest-core-1.3.jar"
    AUDIT_SDK_JAR="$AUDIT_ANDROID_HOME/platforms/android-36.1/android.jar"
    for dependency in "$AUDIT_JUNIT" "$AUDIT_HAMCREST" "$AUDIT_SDK_JAR"; do test -f "$dependency"; done
    AUDIT_CP="$AUDIT_JUNIT:$AUDIT_HAMCREST:$AUDIT_SDK_JAR"
    mkdir -p "$AUDIT_RUN_ROOT/classes"
    set --
    for name in OriginPolicy ConnectionMode ConnectionMonitor ConnectionRecovery ConnectionRouter ConnectionPlan; do
      set -- "$@" "android/app/src/main/java/com/cloudgeek/spool/android/$name.java" "android/app/src/test/java/com/cloudgeek/spool/android/${name}Test.java"
    done
    "$AUDIT_JAVA_HOME/bin/javac" "-J-Djava.io.tmpdir=$TMPDIR" -encoding UTF-8 -source 17 -target 17 -Xlint:-options -cp "$AUDIT_CP" -d "$AUDIT_RUN_ROOT/classes" "$@" android/app/src/main/java/com/cloudgeek/spool/android/RelayPairing.java
    "$AUDIT_JAVA_HOME/bin/java" -Djava.io.tmpdir="$TMPDIR" -cp "$AUDIT_RUN_ROOT/classes:$AUDIT_CP" org.junit.runner.JUnitCore \
      com.cloudgeek.spool.android.OriginPolicyTest \
      com.cloudgeek.spool.android.ConnectionModeTest \
      com.cloudgeek.spool.android.ConnectionMonitorTest \
      com.cloudgeek.spool.android.ConnectionRecoveryTest \
      com.cloudgeek.spool.android.ConnectionRouterTest \
      com.cloudgeek.spool.android.ConnectionPlanTest
    ;;
  *)
    echo 'Usage: sh tools/security-audit/run.sh [repro|android|web-ingress|regression|web|java]' >&2
    exit 2
    ;;
esac
