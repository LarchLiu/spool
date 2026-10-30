#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
# The outer invocation owns the temporary directories until all build tools exit.
if [ "${SPOOL_ANDROID_BUILD_RUN:-}" != 1 ]; then
    exec sh "$ROOT/android/tools/run.sh" env SPOOL_ANDROID_BUILD_RUN=1 sh "$ROOT/android/tools/build.sh" "$@"
fi
cd "$ROOT"
export ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
if [ -z "${JAVA_HOME:-}" ] && [ -d '/Applications/Android Studio.app/Contents/jbr/Contents/Home' ]; then
    export JAVA_HOME='/Applications/Android Studio.app/Contents/jbr/Contents/Home'
fi
NDK_VERSION=29.0.14206865
export ANDROID_NDK_HOME=${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/$NDK_VERSION}
if [ ! -d "$ANDROID_NDK_HOME/toolchains/llvm" ]; then
    echo "Missing Android NDK: $ANDROID_NDK_HOME" >&2
    echo "Install NDK $NDK_VERSION in Android Studio > SDK Manager > SDK Tools, or set ANDROID_NDK_HOME." >&2
    exit 1
fi
mkdir -p "$ROOT/build/android/tools"
export GOBIN="$ROOT/build/android/tools"
export PATH="$GOBIN:${JAVA_HOME:+$JAVA_HOME/bin:}$PATH"
echo 'Building gomobile tools...'
go -C android/transport install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
TARGET=${SPOOL_ANDROID_TARGET:-android/arm64,android/amd64}
echo "Building Go networking AAR for $TARGET..."
(
    cd android/transport
    gomobile bind -target="$TARGET" -androidapi=26 \
        -ldflags='-s -w' -o "$TMPDIR/spoolnet.aar" ./spoolnet
)
mv "$TMPDIR/spoolnet.aar" "$ROOT/build/android/spoolnet.aar"
mv "$TMPDIR/spoolnet-sources.jar" "$ROOT/build/android/spoolnet-sources.jar"
if [ ! -f "$ROOT/build/android/debug.keystore" ]; then
    "${JAVA_HOME:+$JAVA_HOME/bin/}keytool" -genkeypair -keystore "$ROOT/build/android/debug.keystore" \
        -storepass android -keypass android -alias androiddebugkey -keyalg RSA -keysize 2048 \
        -validity 10000 -dname 'CN=Android Debug,O=Android,C=US'
fi
if [ "$#" -eq 0 ]; then set -- :app:assembleDebug :app:testDebugUnitTest :app:lintDebug; fi
echo "Running Android build tasks: $*"
sh "$ROOT/android/tools/gradle.sh" "$@"
