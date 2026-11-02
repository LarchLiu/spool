#!/usr/bin/env python3
"""Measure targeted flings on the focused debug chat without content capture or touch injection."""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time
import uuid
from pathlib import Path

from performance_probe import foreground_snapshot, wait_finished_art_trace


def main():
    root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--rounds", type=int, default=12)
    parser.add_argument("--direction", choices=("alternate", "up", "down"), default="alternate")
    parser.add_argument("--method-sampling-us", type=int, default=0,
                        help="Optional ART method sampling; adds overhead, use for attribution only")
    parser.add_argument("--allow-hydrating", action="store_true")
    parser.add_argument("--wait-history-seconds", type=int, default=0,
                        help="Wait up to 300 seconds for this same foreground chat to finish hydration")
    parser.add_argument("--pause-seconds", type=float, default=1.25,
                        help="Pause between flings; a 7000px/s fling decelerates for about two seconds, so "
                             "pauses of three seconds or more emulate reading between gestures")
    parser.add_argument("--output", type=Path, default=root / "build/artifacts/android-native/device-performance-foreground.json")
    args = parser.parse_args()
    if args.method_sampling_us and not 1000 <= args.method_sampling_us <= 100000:
        parser.error("--method-sampling-us must be between 1000 and 100000")
    if not 0 <= args.wait_history_seconds <= 300:
        parser.error("--wait-history-seconds must be between 0 and 300")
    if not 0.25 <= args.pause_seconds <= 10:
        parser.error("--pause-seconds must be between 0.25 and 10")
    if not os.environ.get("TMPDIR", "").startswith(str(root / "build/tmp/")):
        raise SystemExit("Run through sh android/tools/run.sh")
    sdk = Path(os.environ.get("ANDROID_HOME", os.environ.get("ANDROID_SDK_ROOT", str(Path.home() / "Library/Android/sdk"))))
    executable = shutil.which("adb") or str(sdk / "platform-tools/adb")
    package = "com.cloudgeek.spool.android.dev"
    component = package + "/com.cloudgeek.spool.android.MainActivity"

    def adb(*command):
        return subprocess.run([executable, "-s", args.serial, *command], check=True, capture_output=True, text=True, timeout=30).stdout

    def snapshot(expected=None, velocity=None, allow_hydrating=None):
        action = () if velocity is None else ("fling", str(velocity), expected["probeToken"], str(expected["selectionEpoch"]))
        result = adb("shell", "dumpsys", "activity", component, "spool-performance", *action)
        return foreground_snapshot(result, expected, velocity is not None,
                                   args.allow_hydrating if allow_hydrating is None else allow_hydrating)

    device_trace = None
    profiling = False
    try:
        before = snapshot(allow_hydrating=args.allow_hydrating or args.wait_history_seconds > 0)
        deadline = time.monotonic() + args.wait_history_seconds
        while before.get("historyHasMore") and not args.allow_hydrating:
            if time.monotonic() >= deadline:
                raise ValueError("The same foreground chat is still hydrating; no gestures were issued")
            time.sleep(min(5, max(0, deadline - time.monotonic())))
            before = snapshot(before, allow_hydrating=True)
        if args.method_sampling_us:
            # Android's system_server cannot write app/external-storage contexts.
            # This device-only exception is removed in finally; host scratch stays in build/tmp.
            device_trace = "/data/local/tmp/spool-scroll-" + uuid.uuid4().hex + ".trace"
            response = adb("shell", "am", "profile", "start", "--sampling", str(args.method_sampling_us), package, device_trace)
            if "Error" in response:
                raise ValueError(response.strip())
            profiling = True
        adb("shell", "dumpsys", "gfxinfo", package, "reset")
        samples = []
        for turn in range(max(1, min(60, args.rounds))):
            start = time.monotonic()
            velocity = -7000 if args.direction == "up" or args.direction == "alternate" and turn % 2 == 0 else 7000
            stats = snapshot(before, velocity)
            stats["commandMs"] = round((time.monotonic() - start) * 1000, 3)
            samples.append(stats)
            time.sleep(args.pause_seconds)
        raw = adb("shell", "dumpsys", "gfxinfo", package)
        frames = {}
        for line in raw.splitlines():
            match = re.match(r"\s*(Total frames rendered|Janky frames|\d+th percentile|Number Slow UI thread|Number Slow bitmap uploads|Number Slow issue draw commands):\s*(.+)", line)
            if match:
                frames[match[1]] = match[2]
        result = {
            "scope": "One foreground, focused debug chat; gestures bind to its Activity token and selection epoch; no touch injection or backend writes",
            "before": before,
            "after": snapshot(before),
            "flings": samples,
            "frameStats": frames,
            "allowHydrating": args.allow_hydrating,
            "direction": args.direction,
            "pauseSeconds": args.pause_seconds,
            "methodSamplingUs": args.method_sampling_us,
            "limitations": "Memory and gfxinfo counters cover the process, not only the selected Activity. Not finger-to-photon latency or Web/native raster parity.",
        }
        if profiling:
            adb("shell", "am", "profile", "stop", package)
            profiling = False
            local_trace = Path(os.environ["TMPDIR"]) / "scroll.trace"
            wait_finished_art_trace(lambda: adb("pull", device_trace, str(local_trace)), local_trace)
            summary = subprocess.run([sys.executable, str(root / "android/tools/summarize-method-trace.py"), str(local_trace)],
                                     check=True, capture_output=True, text=True, timeout=60)
            result["methodTrace"] = json.loads(summary.stdout)
            result["methodTrace"].pop("trace", None)
    except subprocess.CalledProcessError as error:
        raise SystemExit((error.stderr or error.stdout or str(error)).strip()) from error
    except (ValueError, subprocess.SubprocessError) as error:
        raise SystemExit(str(error)) from error
    finally:
        try:
            if profiling:
                adb("shell", "am", "profile", "stop", package)
        finally:
            if device_trace is not None:
                adb("shell", "rm", "-f", "--", device_trace)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
