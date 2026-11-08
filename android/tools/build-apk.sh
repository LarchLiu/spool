#!/bin/sh
set -eu
MODE=${1:-debug}
if [ "$#" -gt 1 ]; then
    echo 'Usage: sh android/tools/build-apk.sh [debug|release]' >&2
    exit 2
fi
case "$MODE" in
    debug|release) ;;
    *) echo 'Usage: sh android/tools/build-apk.sh [debug|release]' >&2; exit 2 ;;
esac

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "${SPOOL_ANDROID_APK_RUN:-}" != 1 ]; then
    exec sh "$ROOT/android/tools/run.sh" env SPOOL_ANDROID_APK_RUN=1 sh "$ROOT/android/tools/build-apk.sh" "$MODE"
fi
cd "$ROOT"
export ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
if [ -z "${JAVA_HOME:-}" ] && [ -d '/Applications/Android Studio.app/Contents/jbr/Contents/Home' ]; then
    export JAVA_HOME='/Applications/Android Studio.app/Contents/jbr/Contents/Home'
fi
export PATH="${JAVA_HOME:+$JAVA_HOME/bin:}$PATH"

if [ "$MODE" = release ]; then
    SIGNING_ENV=${SPOOL_ANDROID_SIGNING_ENV:-$ROOT/android/signing/github-secrets.env}
    KEYSTORE=${ANDROID_KEYSTORE_FILE:-$ROOT/android/signing/spool-release.p12}
    if [ ! -f "$SIGNING_ENV" ]; then
        echo "Missing Android signing configuration: $SIGNING_ENV" >&2
        exit 1
    fi
    if [ ! -f "$KEYSTORE" ]; then
        echo "Missing Android release keystore: $KEYSTORE" >&2
        exit 1
    fi
    SPOOL_ANDROID_BUILD_RUN=1 sh android/tools/build.sh :app:assembleRelease :app:lintRelease
else
    SPOOL_ANDROID_BUILD_RUN=1 sh android/tools/build.sh :app:assembleDebug :app:lintDebug
fi

APK_DIR="$ROOT/build/android/gradle/app/outputs/apk/$MODE"
if [ "$MODE" = debug ]; then
    APK="$APK_DIR/app-debug.apk"
else
    APK="$APK_DIR/app-release-unsigned.apk"
fi
python3 android/tools/check-syntax-package.py --apk "$APK" --abis arm64-v8a

if [ "$MODE" = release ]; then
    # Load signing secrets only after the build tools have exited.
    set -a
    . "$SIGNING_ENV"
    set +a
    : "${ANDROID_KEYSTORE_PASSWORD:?Set ANDROID_KEYSTORE_PASSWORD in the signing configuration}"
    : "${ANDROID_KEY_ALIAS:?Set ANDROID_KEY_ALIAS in the signing configuration}"
    : "${ANDROID_KEY_PASSWORD:?Set ANDROID_KEY_PASSWORD in the signing configuration}"
    export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djava.io.tmpdir=$TMPDIR"
    TOOLS="$ANDROID_HOME/build-tools/36.1.0"
    "$TOOLS/zipalign" -P 16 -f 4 "$APK" "$TMPDIR/aligned.apk"
    "$TOOLS/apksigner" sign --ks "$KEYSTORE" --ks-key-alias "$ANDROID_KEY_ALIAS" \
        --ks-pass env:ANDROID_KEYSTORE_PASSWORD --key-pass env:ANDROID_KEY_PASSWORD \
        --v4-signing-enabled false --out "$TMPDIR/signed.apk" "$TMPDIR/aligned.apk"
    "$TOOLS/apksigner" verify --verbose "$TMPDIR/signed.apk"
    "$TOOLS/zipalign" -c -P 16 4 "$TMPDIR/signed.apk"
    VERSION=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["elements"][0]["versionName"])' "$APK_DIR/output-metadata.json")
    APK="$ROOT/build/android/spool-$VERSION-android-arm64.apk"
    mv "$TMPDIR/signed.apk" "$APK"
fi

printf '\nAPK: %s\n' "$APK"
