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
fetch gson-2.13.1.jar \
    https://repo.maven.apache.org/maven2/com/google/code/gson/gson/2.13.1/gson-2.13.1.jar \
    94855942d4992f112946d3de1c334e709237b8126d8130bf07807c018a4a2120
python3 android/tools/prepare-textmate.py "$CACHE/tm4e-core-0.14.1.jar" "$TMPDIR/textmate.jar"
python3 android/tools/build-syntax-native.py --host
fetch commonmark-0.24.0.jar \
    https://repo.maven.apache.org/maven2/org/commonmark/commonmark/0.24.0/commonmark-0.24.0.jar \
    679338e0b7fc15c02d275d598654b01a149893bc28a87992e90123c8d06af25b
fetch commonmark-ext-gfm-tables-0.24.0.jar \
    https://repo.maven.apache.org/maven2/org/commonmark/commonmark-ext-gfm-tables/0.24.0/commonmark-ext-gfm-tables-0.24.0.jar \
    b54dc332f931e6d07c2766144c087b08f3693677e368151a67020b4e95bb4b99
fetch commonmark-ext-gfm-strikethrough-0.24.0.jar \
    https://repo.maven.apache.org/maven2/org/commonmark/commonmark-ext-gfm-strikethrough/0.24.0/commonmark-ext-gfm-strikethrough-0.24.0.jar \
    7385cb637f04dc4cbda4ddca9c2fcd2af7ac536a50e4c8d2c77f4748bb14bf41
fetch commonmark-ext-task-list-items-0.24.0.jar \
    https://repo.maven.apache.org/maven2/org/commonmark/commonmark-ext-task-list-items/0.24.0/commonmark-ext-task-list-items-0.24.0.jar \
    9705b53140c68f63136f183e6c90c834e376ae0780e84800f9ec2ea49ecab984
fetch androidsvg-aar-1.4.aar \
    https://repo.maven.apache.org/maven2/com/caverock/androidsvg-aar/1.4/androidsvg-aar-1.4.aar \
    02a5b08a2b35d2d58eb2eaca9d84ac00fb341da725fdbd653ea3ed130437e95a
unzip -p "$CACHE/androidsvg-aar-1.4.aar" classes.jar > "$TMPDIR/androidsvg.jar"
fetch exifinterface-1.4.1.aar \
    https://dl.google.com/dl/android/maven2/androidx/exifinterface/exifinterface/1.4.1/exifinterface-1.4.1.aar \
    17e17a6b86bb54074d3a7ecfa695ab920eaff4cf667a8371ed82e377580cfcf6
unzip -p "$CACHE/exifinterface-1.4.1.aar" classes.jar > "$TMPDIR/exifinterface.jar"
fetch core-1.17.0.aar \
    https://dl.google.com/dl/android/maven2/androidx/core/core/1.17.0/core-1.17.0.aar \
    e1bd22bbcf5cee3a992dedb7d2d3c3b31c9f761c01e56d00ea443c8aaa981182
unzip -p "$CACHE/core-1.17.0.aar" classes.jar > "$TMPDIR/core.jar"
(
    cd android/transport
    go run golang.org/x/mobile/cmd/gobind -lang=java -outdir="$TMPDIR/generated" ./spoolnet
)
export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djava.io.tmpdir=$TMPDIR"
python3 - "$ROOT" "$TMPDIR" <<'PY'
import pathlib, sys, xml.etree.ElementTree as ET
root, scratch = map(pathlib.Path, sys.argv[1:])
ET.register_namespace('android', 'http://schemas.android.com/apk/res/android')
manifest = ET.parse(root / 'android/app/src/main/AndroidManifest.xml')
manifest.getroot().set('package', 'com.cloudgeek.spool.android')
manifest.write(scratch / 'AndroidManifest.xml', encoding='utf-8', xml_declaration=True)
PY
mkdir -p "$TMPDIR/r" "$TMPDIR/classes"
"$AAPT2" compile --dir android/app/src/main/res -o "$TMPDIR/resources.zip"
"$AAPT2" link -I "$SDK_JAR" --manifest "$TMPDIR/AndroidManifest.xml" \
    --java "$TMPDIR/r" --min-sdk-version 26 --target-sdk-version 36 \
    -o "$TMPDIR/resources.ap_" "$TMPDIR/resources.zip"
CP="$SDK_JAR:$CACHE/junit-4.13.2.jar:$CACHE/hamcrest-core-1.3.jar:$CACHE/commonmark-0.24.0.jar:$CACHE/commonmark-ext-gfm-tables-0.24.0.jar:$CACHE/commonmark-ext-gfm-strikethrough-0.24.0.jar:$CACHE/commonmark-ext-task-list-items-0.24.0.jar:$TMPDIR/androidsvg.jar:$TMPDIR/exifinterface.jar:$TMPDIR/core.jar:$TMPDIR/textmate.jar:$CACHE/gson-2.13.1.jar"
python3 - "$JAVAC" "$CP" "$TMPDIR" "$ROOT" <<'PY'
import pathlib, subprocess, sys
javac, cp, scratch, root = sys.argv[1:]
scratch, root = pathlib.Path(scratch), pathlib.Path(root)
folders = [scratch / 'generated/java', scratch / 'r', root / 'android/app/src/main/java', root / 'android/app/src/test/java', root / 'android/textmate/src/main/java']
sources = [str(p) for folder in folders for p in folder.rglob('*.java')]
subprocess.run([javac, '-encoding', 'UTF-8', '-source', '17', '-target', '17', '-Xlint:-options', '-cp', cp,
                '-d', str(scratch / 'classes'), *sources], check=True)
PY
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
