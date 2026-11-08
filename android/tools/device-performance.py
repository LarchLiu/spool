#!/usr/bin/env python3
"""Measure targeted flings on the focused debug chat without content capture or touch injection."""
import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import time
import uuid
from pathlib import Path

from performance_probe import foreground_snapshot, reached_edge, validate_interval, validate_target, validate_visible_content, wait_finished_art_trace


def main():
    root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--rounds", type=int, default=12)
    parser.add_argument("--direction", choices=("alternate", "up", "down"), default="alternate")
    parser.add_argument("--traversal", choices=("bottom-to-top", "top-to-bottom", "round-trip"),
                        help="Continue to native scroll boundaries; round-trip goes bottom/top/bottom")
    parser.add_argument("--verify-position", type=int, action="append", default=[],
                        help="Compare visible content and geometry at this row before/after traversal; may be repeated")
    parser.add_argument("--chat-title", help="Open the one exact matching chat title in the focused debug Activity")
    parser.add_argument("--expand-tools", action="store_true", help="Apply the reproducible tools/images preset before measuring")
    parser.add_argument("--start-position", type=int, help="Align this recycler row to the viewport top before measuring")
    parser.add_argument("--activity-reuse", choices=("on", "off"), help="Debug A/B for tool widget reuse; restored after the run")
    parser.add_argument("--warmup-rounds", type=int, default=0, help="Warm this same interval before resetting to --start-position")
    parser.add_argument("--settle-seconds", type=float, default=2.5, help="Allow positioning and the final fling to finish")
    parser.add_argument("--method-sampling-us", type=int, default=0,
                        help="Optional ART method sampling; adds overhead, use for attribution only")
    parser.add_argument("--allow-hydrating", action="store_true")
    parser.add_argument("--min-parts", type=int, default=0,
                        help="Require at least this many transcript parts before issuing gestures")
    parser.add_argument("--expect-renderer", choices=("native-views", "markwon-recycler"),
                        help="Require the selected rendering implementation before issuing gestures")
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
    if args.min_parts < 0:
        parser.error("--min-parts must be nonnegative")
    if not 0.25 <= args.pause_seconds <= 10:
        parser.error("--pause-seconds must be between 0.25 and 10")
    if args.start_position is not None and args.start_position < 0:
        parser.error("--start-position must be nonnegative")
    if not 0 <= args.warmup_rounds <= 60 or args.warmup_rounds and args.start_position is None and not args.expand_tools:
        parser.error("--warmup-rounds requires --start-position and must be between 0 and 60")
    if not 2 <= args.settle_seconds <= 10:
        parser.error("--settle-seconds must be between 2 and 10")
    if args.traversal and (args.start_position is not None or args.warmup_rounds or args.method_sampling_us):
        parser.error("--traversal uses edge positioning and requires no warmup or method sampler")
    if args.verify_position and (min(args.verify_position) < 0 or not args.traversal and args.start_position is None):
        parser.error("--verify-position requires --traversal or --start-position and nonnegative positions")
    if not os.environ.get("TMPDIR", "").startswith(str(root / "build/tmp/")):
        raise SystemExit("Run through sh android/tools/run.sh")
    sdk = Path(os.environ.get("ANDROID_HOME", os.environ.get("ANDROID_SDK_ROOT", str(Path.home() / "Library/Android/sdk"))))
    executable = shutil.which("adb") or str(sdk / "platform-tools/adb")
    package = "com.cloudgeek.spool.android.dev"
    component = package + "/com.cloudgeek.spool.android.MainActivity"

    def adb(*command):
        return subprocess.run([executable, "-s", args.serial, *command], check=True, capture_output=True, text=True, timeout=30).stdout

    def snapshot(expected=None, velocity=None, allow_hydrating=None, require_history=True):
        action = () if velocity is None else ("fling", str(velocity), expected["probeToken"], str(expected["selectionEpoch"]))
        result = adb("shell", "dumpsys", "activity", component, "spool-performance", *action)
        return foreground_snapshot(result, expected, velocity is not None,
                                   args.allow_hydrating if allow_hydrating is None else allow_hydrating, require_history)

    def control(expected, command, value, allow_hydrating=None):
        result = adb("shell", "dumpsys", "activity", component, "spool-performance", command, str(value),
                     expected["probeToken"], str(expected["selectionEpoch"]))
        value = foreground_snapshot(result, expected, allow_hydrating=args.allow_hydrating if allow_hydrating is None else allow_hydrating)
        if value.get("controlAccepted") is not True:
            raise ValueError("The focused page rejected the targeted debug control; measurement aborted")
        return value

    def flings(expected, rounds):
        samples = []
        for turn in range(rounds):
            start = time.monotonic()
            velocity = -7000 if args.direction == "up" or args.direction == "alternate" and turn % 2 == 0 else 7000
            stats = snapshot(expected, velocity)
            if args.start_position is not None:
                validate_interval(stats, expected)
            stats["commandMs"] = round((time.monotonic() - start) * 1000, 3)
            samples.append(stats)
            time.sleep(args.pause_seconds)
        time.sleep(max(0, args.settle_seconds - args.pause_seconds))
        return samples

    def position(expected):
        control(expected, "position", args.start_position)
        time.sleep(args.settle_seconds)
        settled = snapshot(expected)
        validate_interval(settled, expected, args.start_position)
        return settled

    def verify_positions(expected):
        checks = {}
        for row in args.verify_position:
            control(expected, "position", row)
            # Includes image and lazy tool-body delivery before the out-of-band digest.
            time.sleep(max(5, args.settle_seconds))
            settled = control(expected, "verify-content", "visible")
            validate_interval(settled, expected, row)
            checks[row] = settled
        return checks

    def traverse(expected):
        directions = ["down"] if args.traversal == "top-to-bottom" else ["up", "down"] if args.traversal == "round-trip" else ["up"]
        samples, legs = [], []
        current = expected
        for direction in directions:
            count = 0
            while not reached_edge(current, direction):
                if count >= 160:
                    raise ValueError("Full traversal did not reach the requested boundary after 160 flings")
                start = time.monotonic()
                current = snapshot(expected, -7000 if direction == "up" else 7000)
                validate_interval(current, expected)
                current["commandMs"] = round((time.monotonic() - start) * 1000, 3)
                current["direction"] = direction
                samples.append(current); count += 1
                if not reached_edge(current, direction):
                    time.sleep(args.pause_seconds)
            legs.append({"direction": direction, "flings": count, "reachedEdge": True,
                         "firstVisible": current["viewport"]["firstVisible"], "lastVisible": current["viewport"]["lastVisible"]})
            print(f"Reached {direction} boundary after {count} flings", file=sys.stderr, flush=True)
        time.sleep(args.settle_seconds)
        return samples, legs

    device_trace = None
    profiling = False
    restore_reuse = None
    target = None
    scenario = None
    verification_errors = []
    try:
        if args.chat_title:
            initial = snapshot(allow_hydrating=True, require_history=False)
            activity_token = initial["probeToken"]
            deadline = time.monotonic() + 60
            while True:
                response = adb("shell", "dumpsys", "activity", component, "spool-performance", "open-title", shlex.quote(args.chat_title),
                               initial["probeToken"], str(initial["selectionEpoch"]))
                opened = foreground_snapshot(response, allow_hydrating=True, require_history=False)
                if opened["probeToken"] != activity_token:
                    raise ValueError("The Activity changed while opening the named chat")
                if opened.get("controlAccepted") is True:
                    break
                if time.monotonic() >= deadline:
                    raise ValueError("The exact chat title was not uniquely available in this focused Activity")
                time.sleep(1)
                initial = snapshot(allow_hydrating=True, require_history=False)
                if initial["probeToken"] != activity_token:
                    raise ValueError("The Activity changed while waiting for the chat list")
            deadline = time.monotonic() + max(60, args.wait_history_seconds)
            history_retries = 0
            while not opened.get("historyVisible") or not opened.get("turns") or opened.get("historyHasMore") or opened.get("renderPending", 0):
                if time.monotonic() >= deadline:
                    raise ValueError("The selected chat did not finish loading before the history deadline")
                if opened.get("historyFailed") and history_retries < 3:
                    opened = control(opened, "history-retry", "retry", allow_hydrating=True)
                    history_retries += 1
                time.sleep(1)
                opened = snapshot(opened, allow_hydrating=True, require_history=False)
        before = snapshot(allow_hydrating=args.allow_hydrating or args.wait_history_seconds > 0)
        deadline = time.monotonic() + args.wait_history_seconds
        while (before.get("historyHasMore") or before.get("renderPending", 0)) and not args.allow_hydrating:
            if time.monotonic() >= deadline:
                raise ValueError("The same foreground chat is still hydrating; no gestures were issued")
            time.sleep(min(5, max(0, deadline - time.monotonic())))
            before = snapshot(before, allow_hydrating=True)
        validate_target(before, args.min_parts, args.expect_renderer)
        target = before
        if args.expand_tools:
            scenario = control(before, "expand-tools", "standard")["scenario"]
            if args.start_position is None and not args.traversal:
                args.start_position = scenario["startPosition"]
            time.sleep(args.settle_seconds)
            before = snapshot(before)
        if args.activity_reuse is not None:
            enabled = before.get("viewport", {}).get("activityRowReuse")
            if type(enabled) is not bool:
                raise ValueError("Use a debug APK with activity reuse controls")
            control(before, "activity-reuse", args.activity_reuse)
            restore_reuse = "on" if enabled else "off"
        verification_before = verify_positions(before)
        if args.traversal:
            start_edge = "up" if args.traversal == "top-to-bottom" else "down"
            control(before, "position", 0 if start_edge == "up" else before["virtualRows"] - 1)
            time.sleep(args.settle_seconds)
            settled = snapshot(before); validate_interval(settled, before); before = settled
            if not reached_edge(before, start_edge):
                raise ValueError("The full traversal did not start at the requested native boundary")
            print(f"Starting {args.traversal}: {before['virtualRows']} rows, {before['parts']} parts", file=sys.stderr, flush=True)
        if args.start_position is not None:
            before = position(before)
        if args.warmup_rounds:
            flings(before, args.warmup_rounds)
            before = position(before)
        if args.method_sampling_us:
            # Android's system_server cannot write app/external-storage contexts.
            # This device-only exception is removed in finally; host scratch stays in build/tmp.
            device_trace = "/data/local/tmp/spool-scroll-" + uuid.uuid4().hex + ".trace"
            response = adb("shell", "am", "profile", "start", "--sampling", str(args.method_sampling_us), package, device_trace)
            if "Error" in response:
                raise ValueError(response.strip())
            profiling = True
        adb("shell", "dumpsys", "gfxinfo", package, "reset")
        measured_started = time.monotonic()
        if args.traversal:
            samples, legs = traverse(before)
        else:
            samples, legs = flings(before, max(1, min(60, args.rounds))), []
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
            "startPosition": args.start_position,
            "activityReuse": args.activity_reuse,
            "warmupRounds": args.warmup_rounds,
            "settleSeconds": args.settle_seconds,
            "scenario": scenario,
            "traversal": args.traversal,
            "legs": legs,
            "elapsedSeconds": round(time.monotonic() - measured_started, 3),
            "minParts": args.min_parts,
            "expectedRenderer": args.expect_renderer,
            "limitations": "Memory and gfxinfo counters cover the process, not only the selected Activity. Not finger-to-photon latency or Web/native raster parity.",
        }
        if args.start_position is not None:
            validate_interval(result["after"], before)
        if args.traversal or verification_before:
            validate_interval(result["after"], before)
            verification_after = verify_positions(before)
            result["contentVerification"] = []
            for row, initial in verification_before.items():
                current = verification_after[row]
                matched = True
                try:
                    validate_visible_content(initial, current)
                except ValueError as error:
                    matched = False; verification_errors.append(str(error))
                result["contentVerification"].append({"position": row, "matched": matched,
                                                      "before": initial["visibleContent"], "after": current["visibleContent"]})
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
            if restore_reuse is not None:
                control(target, "activity-reuse", restore_reuse)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))
    if verification_errors:
        raise SystemExit("; ".join(verification_errors))


if __name__ == "__main__":
    main()
