#!/bin/sh
# Optional, explicit tool installation for the macOS development host.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "${SPOOL_ANDROID_NDK_RUN:-}" != 1 ]; then
    exec sh "$ROOT/android/tools/run.sh" env SPOOL_ANDROID_NDK_RUN=1 sh "$ROOT/android/tools/install-ndk.sh"
fi
if [ "$(uname -s)" != Darwin ]; then
    echo 'Install NDK 29.0.14206865 with Android Studio SDK Manager on this OS.' >&2
    exit 1
fi
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}
DEST="$SDK/ndk/29.0.14206865"
if [ -e "$DEST" ]; then
    echo "Already present; leaving unchanged: $DEST"
    exit 0
fi
AVAILABLE_KB=$(df -Pk "$ROOT" | awk 'NR == 2 { print $4 }')
if [ "$AVAILABLE_KB" -lt 8388608 ]; then
    echo 'NDK download/extraction needs at least 8 GiB free on the checkout volume; leaving SDK unchanged.' >&2
    exit 1
fi
ARCHIVE="$TMPDIR/android-ndk-r29-darwin.zip"
curl --fail --location --retry 2 https://dl.google.com/android/repository/android-ndk-r29-darwin.zip -o "$ARCHIVE"
echo "03d29fbb57e3c05a7d53597dd011d856c1456a4f  $ARCHIVE" | shasum -a 1 -c -
unzip -q "$ARCHIVE" -d "$TMPDIR/ndk" < /dev/null
mkdir -p "$SDK/ndk"
mv "$TMPDIR/ndk/android-ndk-r29" "$DEST"
echo "Installed $DEST"
