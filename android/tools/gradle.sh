#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
export ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
if [ -z "${JAVA_HOME:-}" ] && [ -d '/Applications/Android Studio.app/Contents/jbr/Contents/Home' ]; then
    JAVA_HOME='/Applications/Android Studio.app/Contents/jbr/Contents/Home'
    export JAVA_HOME
fi
JAVA=${JAVA_HOME:+$JAVA_HOME/bin/}java
export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djava.io.tmpdir=$TMPDIR"
cd "$ROOT/android"
exec "$JAVA" -classpath "$ROOT/android/gradle/wrapper/gradle-wrapper.jar" \
    org.gradle.wrapper.GradleWrapperMain \
    --project-cache-dir "$ROOT/build/cache/gradle-project/android" "$@"
