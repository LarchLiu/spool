#!/usr/bin/env python3
"""Launch the real Activity in an owned, R8-shrunk, non-debuggable synthetic release APK."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import uuid


def main():
    root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    args = parser.parse_args()
    if not os.environ.get("TMPDIR", "").startswith(str(root / "build/tmp/")):
        raise SystemExit("Run through sh android/tools/run.sh")
    sdk = Path(os.environ.get("ANDROID_HOME", os.environ.get("ANDROID_SDK_ROOT", str(Path.home() / "Library/Android/sdk"))))
    apk = root / "build/android/gradle/app/outputs/apk/releaseVerify/app-releaseVerify.apk"
    output = root / "build/artifacts/android-markwon-release/device-smoke.json"
    package = "com.cloudgeek.spool.android.releaseverify"
    component = package + "/com.cloudgeek.spool.android.MainActivity"

    def command(*values, check=True):
        result = subprocess.run([str(value) for value in values], capture_output=True, text=True, timeout=45)
        if check and result.returncode:
            raise RuntimeError((result.stderr or result.stdout).strip())
        return result

    def adb(*values, check=True):
        return command(sdk / "platform-tools/adb", "-s", args.serial, *values, check=check)

    if adb("shell", "pm", "list", "packages", package).stdout.strip():
        raise SystemExit("releaseverify is already installed; refusing to replace or remove an unowned installation")
    badging = command(sdk / "build-tools/36.1.0/aapt2", "dump", "badging", apk).stdout
    if "name='" + package + "'" not in badging or "application-debuggable" in badging:
        raise SystemExit("Verification APK must have its isolated package ID and be non-debuggable")
    if "android.permission.INTERNET" in badging:
        raise SystemExit("Verification APK must have no Internet permission")
    token = str(uuid.uuid4())
    summary = {"package": package, "apkSha256": hashlib.sha256(apk.read_bytes()).hexdigest(),
               "api": adb("shell", "getprop", "ro.build.version.sdk").stdout.strip(),
               "model": adb("shell", "getprop", "ro.product.model").stdout.strip(),
               "internetPermission": False, "removed": False}
    if int(summary["api"]) < 29:
        raise SystemExit("The committed-frame device smoke requires Android API29 or later")
    installed = False
    try:
        if "Success" not in adb("install", "--no-incremental", apk).stdout:
            raise RuntimeError("Verification installation was not confirmed")
        installed = True
        adb("shell", "am", "start", "-W", "-n", component, "--ez", "spool_markwon", "false",
            "--es", "release_probe_token", token)
        deadline = time.monotonic() + 35
        while True:
            pid = adb("shell", "pidof", package, check=False).stdout.strip()
            if re.fullmatch(r"\d+", pid):
                log = adb("logcat", "--pid=" + pid, "-d", "-v", "raw", "-s", "SpoolReleaseVerify:I").stdout
                for line in log.splitlines():
                    try:
                        value = json.loads(line)
                    except ValueError:
                        continue
                    if value.get("token") == token:
                        value.pop("token", None)
                        summary["startup"] = value
            if "startup" in summary:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("Release startup/rendering did not complete")
            time.sleep(.4)
        result = summary["startup"]
        if result.get("error"):
            raise RuntimeError(result["error"])
        expected = ("defaultMarkwon", "legacyPreferenceIgnored", "legacyIntentIgnored", "markdownRendered", "codeBlockRendered",
                    "tableRendered", "tableCellsSelectable", "codeSyntaxHighlighted", "fileSourceSyntaxHighlighted", "frameCommitted")
        if result.get("debuggable") is not False or any(result.get(key) is not True for key in expected):
            raise RuntimeError("Release renderer assertions failed")
        probe = adb("shell", "dumpsys", "activity", component, "spool-performance").stdout
        if "SPOOL_PERF " in probe:
            raise RuntimeError("The debug performance entry point was exposed by a release APK")
        summary["performanceProbeBlocked"] = True
        print("Non-debuggable release Activity rendered Markdown, selectable table cells, highlighted answer code and file source, and committed a frame; legacy overrides ignored; performance probe blocked.")
    except Exception as error:
        summary["error"] = str(error)
        raise
    finally:
        if installed:
            adb("shell", "am", "force-stop", package, check=False)
            removed = adb("uninstall", package, check=False)
            summary["removed"] = removed.returncode == 0 and "Success" in removed.stdout
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(summary, indent=2) + "\n")
    if not summary["removed"]:
        raise RuntimeError("Owned verification package cleanup requires attention")
    print("Owned verification package removed; report:", output)


if __name__ == "__main__":
    main()
