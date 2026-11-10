#!/bin/sh
# Type-check the real generated bindings/resources and run host-native syntax tests.
# This builds a host JNI library, NOT Android native libraries or an installable APK.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "${SPOOL_ANDROID_JAVA_RUN:-}" != 1 ]; then
    exec sh "$ROOT/android/tools/run.sh" env SPOOL_ANDROID_JAVA_RUN=1 sh "$ROOT/android/tools/check-java.sh"
fi
cd "$ROOT"
export ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
if [ -z "${JAVA_HOME:-}" ] && [ -d '/Applications/Android Studio.app/Contents/jbr/Contents/Home' ]; then
    export JAVA_HOME='/Applications/Android Studio.app/Contents/jbr/Contents/Home'
fi
JAVA=${JAVA_HOME:+$JAVA_HOME/bin/}java
JAVAC=${JAVA_HOME:+$JAVA_HOME/bin/}javac
SDK_JAR="$ANDROID_HOME/platforms/android-36.1/android.jar"
AAPT2="$ANDROID_HOME/build-tools/36.1.0/aapt2"
if [ ! -f "$SDK_JAR" ] || [ ! -x "$AAPT2" ]; then
    echo 'This check needs SDK Platform 36.1 and Build Tools 36.1.0.' >&2
    exit 1
fi
CACHE="$ROOT/build/cache/android-java"
mkdir -p "$CACHE"
fetch() {
    NAME=$1 URL=$2 HASH=$3
    if [ ! -f "$CACHE/$NAME" ]; then
        curl --fail --location --silent --show-error "$URL" -o "$TMPDIR/$NAME"
        echo "$HASH  $TMPDIR/$NAME" | shasum -a 256 -c -
        mv "$TMPDIR/$NAME" "$CACHE/$NAME"
    fi
    echo "$HASH  $CACHE/$NAME" | shasum -a 256 -c -
}
fetch junit-4.13.2.jar \
    https://repo.maven.apache.org/maven2/junit/junit/4.13.2/junit-4.13.2.jar \
    8e495b634469d64fb8acfa3495a065cbacc8a0fff55ce1e31007be4c16dc57d3
fetch hamcrest-core-1.3.jar \
    https://repo.maven.apache.org/maven2/org/hamcrest/hamcrest-core/1.3/hamcrest-core-1.3.jar \
    66fdef91e9739348df7a096aa384a5685f4e875584cce89386a7a47251c4d8e9
fetch tm4e-core-0.14.1.jar \
    https://repo.maven.apache.org/maven2/org/netbeans/external/org.eclipse.tm4e.core-0.14.1/RELEASE300/org.eclipse.tm4e.core-0.14.1-RELEASE300.jar \
    d78182d6362657a745236d1a085ac2ee11a958f9c3354d8faa87e36563e16174
bun android/tools/prepare-textmate.mjs "$CACHE/tm4e-core-0.14.1.jar" "$TMPDIR/textmate.jar"
bun android/tools/build-syntax-native.mjs --host
sh android/gradlew :app:writeHostJavaClasspath "-PspoolHostJavaClasspath=$TMPDIR/dependencies.json"
APP_CP=$(bun android/tools/check-java.mjs classpath "$TMPDIR/dependencies.json" "$TMPDIR")
(
    cd android/transport
    go run golang.org/x/mobile/cmd/gobind -lang=java -outdir="$TMPDIR/generated" ./spoolnet
)
export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djava.io.tmpdir=$TMPDIR"
bun android/tools/check-java.mjs manifest "$ROOT" "$TMPDIR"
mkdir -p "$TMPDIR/r" "$TMPDIR/classes"
"$AAPT2" compile --dir android/app/src/main/res -o "$TMPDIR/resources.zip"
"$AAPT2" link -I "$SDK_JAR" --manifest "$TMPDIR/AndroidManifest.xml" \
    --java "$TMPDIR/r" --min-sdk-version 26 --target-sdk-version 36 \
    -o "$TMPDIR/resources.ap_" "$TMPDIR/resources.zip"
CP="$SDK_JAR:$CACHE/junit-4.13.2.jar:$CACHE/hamcrest-core-1.3.jar:$TMPDIR/textmate.jar:$APP_CP"
bun android/tools/check-java.mjs compile "$JAVAC" "$CP" "$TMPDIR" "$ROOT"
HOST_SUFFIX=so
if [ "$(uname -s)" = Darwin ]; then HOST_SUFFIX=dylib; fi
"$JAVA" -Dspool.syntax.library="$ROOT/build/android/syntax/host/libspool-textmate.$HOST_SUFFIX" -cp "$TMPDIR/classes:$CP:$ROOT/android/app/src/test/resources" \
    org.junit.runner.JUnitCore com.cloudgeek.spool.android.OriginPolicyTest \
    com.cloudgeek.spool.android.ConnectionMonitorTest \
    com.cloudgeek.spool.android.ConnectionRecoveryTest \
    com.cloudgeek.spool.android.NativeChatRefreshTest \
    com.cloudgeek.spool.android.NativeChatEventsTest \
    com.cloudgeek.spool.android.NativeChatEventsOracleTest \
    com.cloudgeek.spool.android.NativeWorkspaceEventsTest \
    com.cloudgeek.spool.android.NativeHistoryLoaderTest \
    com.cloudgeek.spool.android.NativeArchitectureTest \
    com.cloudgeek.spool.android.NativeFileTargetTest \
    com.cloudgeek.spool.android.NativeExternalLinkTest \
    com.cloudgeek.spool.android.NativeGoalCommandTest \
    com.cloudgeek.spool.android.NativeSyntaxEngineTest \
    org.eclipse.tm4e.core.internal.oniguruma.NativeOnigurumaTest
echo 'PASS: actual generated Java API, Android resources, Java shell and host-native syntax; no Android native library/APK built.'
