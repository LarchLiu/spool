#!/usr/bin/env python3
"""Summarize a non-streaming, dual-clock ART sampled trace without app data."""
import argparse
import collections
import json
from pathlib import Path
import struct

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("trace", type=Path)
parser.add_argument("--output", type=Path)
args = parser.parse_args()
data = args.trace.read_bytes()
end = data.index(b"*end\n") + 5
header = data[:end].decode("utf-8")
methods, threads, section = {}, {}, ""
for line in header.splitlines():
    if line.startswith("*"):
        section = line
    elif section == "*methods":
        fields = line.split("\t")
        methods[int(fields[0], 16)] = ".".join(fields[1:3])
    elif section == "*threads":
        key, name = line.split("\t", 1)
        threads[int(key)] = name
binary = memoryview(data)[end:]
magic, version, offset = struct.unpack_from("<4sHH", binary)
if magic != b"SLOW" or version != 3 or "clock=dual" not in header:
    raise SystemExit("Expected ART v3 dual-clock non-streaming trace")
record_size = struct.unpack_from("<H", binary, 16)[0]
stacks, last = {}, {}
leaf, inclusive = collections.Counter(), collections.Counter()
cpu_leaf, cpu_inclusive = collections.Counter(), collections.Counter()
app_branches = collections.defaultdict(collections.Counter)
for at in range(offset, len(binary) - record_size + 1, record_size):
    tid, encoded, cpu, wall = struct.unpack_from("<HIII", binary, at)
    stack = stacks.setdefault(tid, [])
    if threads.get(tid) == "main" and tid in last and stack:
        duration = max(0, wall - last[tid][1])
        cpu_duration = max(0, cpu - last[tid][0])
        leaf[methods.get(stack[-1], hex(stack[-1]))] += duration
        cpu_leaf[methods.get(stack[-1], hex(stack[-1]))] += cpu_duration
        for method in set(stack):
            inclusive[methods.get(method, hex(method))] += duration
            cpu_inclusive[methods.get(method, hex(method))] += cpu_duration
        # Preserve the immediate callee so a platform-heavy app method is actionable.
        edges = set()
        for index, method in enumerate(stack):
            caller = methods.get(method, hex(method))
            if caller.startswith("com.cloudgeek.spool.android."):
                child = methods.get(stack[index + 1], hex(stack[index + 1])) if index + 1 < len(stack) else "[self]"
                edges.add((caller, child))
        for caller, child in edges:
            app_branches[caller][child] += cpu_duration
    last[tid] = (cpu, wall)
    method, action = encoded & ~3, encoded & 3
    if action == 0:
        stack.append(method)
    elif method in stack:
        while stack and stack.pop() != method:
            pass
result = {
    "trace": str(args.trace),
    "clock": "dual",
    "overflow": "data-file-overflow=true" in header,
    "sampledMainWallMs": round(sum(leaf.values()) / 1000, 3),
    "sampledMainCpuMs": round(sum(cpu_leaf.values()) / 1000, 3),
    "leafWallMs": {key: round(value / 1000, 3) for key, value in leaf.most_common(25)},
    "leafCpuMs": {key: round(value / 1000, 3) for key, value in cpu_leaf.most_common(25)},
    "platformInclusiveCpuMs": {key: round(value / 1000, 3) for key, value in cpu_inclusive.most_common()
                               if key.startswith(("androidx.recyclerview.", "android.widget.TextView.", "android.text.",
                                                  "android.view.ViewRootImpl.", "android.view.ThreadedRenderer."))},
    "appInclusiveWallMs": {key: round(value / 1000, 3) for key, value in inclusive.most_common() if key.startswith("com.cloudgeek.spool.android.")},
    "appInclusiveCpuMs": {key: round(value / 1000, 3) for key, value in cpu_inclusive.most_common() if key.startswith("com.cloudgeek.spool.android.")},
    "appMethodChildrenCpuMs": {key: {child: round(value / 1000, 3) for child, value in app_branches[key].most_common(12)}
                               for key, _ in cpu_inclusive.most_common() if key in app_branches},
}
text = json.dumps(result, indent=2) + "\n"
if args.output:
    args.output.write_text(text)
print(text, end="")
