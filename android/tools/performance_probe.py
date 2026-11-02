"""Validate content-free dumpsys snapshots before measuring a single focused chat."""
import json
import re
import struct
import time


def wait_finished_art_trace(copy_trace, path, attempts=40, interval=.25):
    """Profile stop is asynchronous; reject partial or still-growing ART output."""
    previous = None
    for attempt in range(attempts):
        copy_trace()
        data = path.read_bytes()
        end = data.find(b"*end\n")
        binary = data[end + 5:] if end >= 0 else b""
        complete = False
        if len(binary) >= 18 and b"clock=dual\n" in data[:end + 5]:
            magic, version, offset = struct.unpack_from("<4sHH", binary)
            record_size = struct.unpack_from("<H", binary, 16)[0]
            complete = (magic == b"SLOW" and version == 3 and 18 <= offset <= len(binary)
                        and record_size >= 14 and (len(binary) - offset) % record_size == 0)
        if complete and previous == data:
            return
        previous = data if complete else None
        if attempt + 1 < attempts:
            time.sleep(interval)
    raise ValueError("ART trace did not finish writing after profile stop; incomplete sampling data was not used")


def foreground_snapshot(output, expected=None, require_fling=False, allow_hydrating=False):
    snapshots = []
    for match in re.finditer(r"^SPOOL_PERF (\{[^\r\n]+\})\s*$", output, re.MULTILINE):
        try:
            value = json.loads(match.group(1))
        except ValueError as error:
            raise ValueError("Invalid debug probe JSON") from error
        if not isinstance(value, dict):
            raise ValueError("Invalid debug probe snapshot")
        snapshots.append(value)
    if not snapshots:
        raise ValueError("No responsive Activity probe was returned; check the foreground chat and main-thread responsiveness")
    if any(value.get("probeVersion") != 2 for value in snapshots):
        raise ValueError("Use the current debug APK; a version-2 performance probe is required")
    focused = [value for value in snapshots if value.get("foreground") is True and value.get("focused") is True]
    if len(focused) != 1:
        raise ValueError("Exactly one foreground, focused Activity is required; no background page was selected")
    value = focused[0]
    if value.get("historyVisible") is not True or not value.get("turns"):
        raise ValueError("Open a chat in the foreground; its history must be visible")
    if not re.fullmatch(r"[a-f0-9-]{36}", str(value.get("probeToken", ""))):
        raise ValueError("Missing per-Activity probe token")
    if type(value.get("selectionEpoch")) is not int or value["selectionEpoch"] < 0:
        raise ValueError("Invalid selected-page epoch")
    if expected is not None and any(value.get(key) != expected.get(key) for key in ("probeToken", "selectionEpoch")):
        raise ValueError("The Activity or selected chat changed; measurement aborted")
    if value.get("historyHasMore") is not False and not allow_hydrating:
        raise ValueError("History is still hydrating; wait for it to finish or explicitly allow hydration measurements")
    if require_fling and value.get("flingAccepted") is not True:
        raise ValueError("The focused page rejected the targeted gesture; measurement aborted")
    return value
