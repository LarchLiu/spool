#!/bin/sh
# Type-check the real generated bindings, Android resources and Java shell without an NDK.
# This does NOT build a native library or an installable APK.
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
fetch webkit-1.18.0.aar \
    https://dl.google.com/dl/android/maven2/androidx/webkit/webkit/1.18.0/webkit-1.18.0.aar \
    f15a0bd7d51f4bb8dffdd3a2cf5829f9a7de9ad4ba2682fa85d619032050581f
fetch junit-4.13.2.jar \
    https://repo.maven.apache.org/maven2/junit/junit/4.13.2/junit-4.13.2.jar \
    8e495b634469d64fb8acfa3495a065cbacc8a0fff55ce1e31007be4c16dc57d3
fetch hamcrest-core-1.3.jar \
    https://repo.maven.apache.org/maven2/org/hamcrest/hamcrest-core/1.3/hamcrest-core-1.3.jar \
    66fdef91e9739348df7a096aa384a5685f4e875584cce89386a7a47251c4d8e9
unzip -p "$CACHE/webkit-1.18.0.aar" classes.jar > "$TMPDIR/webkit.jar"
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
CP="$SDK_JAR:$TMPDIR/webkit.jar:$CACHE/junit-4.13.2.jar:$CACHE/hamcrest-core-1.3.jar"
python3 - "$JAVAC" "$CP" "$TMPDIR" "$ROOT" <<'PY'
import pathlib, subprocess, sys
javac, cp, scratch, root = sys.argv[1:]
scratch, root = pathlib.Path(scratch), pathlib.Path(root)
folders = [scratch / 'generated/java', scratch / 'r', root / 'android/app/src/main/java', root / 'android/app/src/test/java']
sources = [str(p) for folder in folders for p in folder.rglob('*.java')]
subprocess.run([javac, '-encoding', 'UTF-8', '-source', '17', '-target', '17', '-Xlint:-options', '-cp', cp,
                '-d', str(scratch / 'classes'), *sources], check=True)
PY
"$JAVA" -cp "$TMPDIR/classes:$CACHE/junit-4.13.2.jar:$CACHE/hamcrest-core-1.3.jar" \
    org.junit.runner.JUnitCore com.cloudgeek.spool.android.OriginPolicyTest \
    com.cloudgeek.spool.android.ConnectionMonitorTest
echo 'PASS: actual generated Java API, Android resources and shell compile; no native library/APK built.'
