#!/usr/bin/env bun
// Summarize a non-streaming dual-clock ART sampled trace without app data.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { cli } from "./common.mjs";
export function summarize(data, trace = "") {
  const end = data.indexOf("*end\n") + 5;
  assert.ok(end >= 5, "Missing trace header");
  const header = data.toString("utf8", 0, end),
    binary = data.subarray(end),
    methods = new Map(),
    threads = new Map();
  let section = "";
  for (const line of header.split(/\r?\n/)) {
    if (line.startsWith("*")) section = line;
    else if (section === "*methods" && line) {
      const fields = line.split("\t");
      methods.set(parseInt(fields[0], 16), fields.slice(1, 3).join("."));
    } else if (section === "*threads" && line) {
      const at = line.indexOf("\t");
      threads.set(Number(line.slice(0, at)), line.slice(at + 1));
    }
  }
  assert.ok(
    binary.length >= 18 &&
      binary.toString("ascii", 0, 4) === "SLOW" &&
      binary.readUInt16LE(4) === 3 &&
      header.includes("clock=dual"),
    "Expected ART v3 dual-clock non-streaming trace",
  );
  const offset = binary.readUInt16LE(6),
    recordSize = binary.readUInt16LE(16);
  assert.ok(
    offset >= 18 &&
      offset <= binary.length &&
      recordSize >= 14 &&
      (binary.length - offset) % recordSize === 0,
    "Incomplete ART trace",
  );
  const stacks = new Map(),
    last = new Map(),
    leaf = new Map(),
    inclusive = new Map(),
    cpuLeaf = new Map(),
    cpuInclusive = new Map(),
    branches = new Map();
  const add = (map, key, value) => map.set(key, (map.get(key) || 0) + value),
    name = (method) => methods.get(method) || "0x" + method.toString(16);
  for (let at = offset; at < binary.length; at += recordSize) {
    const tid = binary.readUInt16LE(at),
      encoded = binary.readUInt32LE(at + 2),
      cpu = binary.readUInt32LE(at + 6),
      wall = binary.readUInt32LE(at + 10);
    if (!stacks.has(tid)) stacks.set(tid, []);
    const stack = stacks.get(tid);
    if (threads.get(tid) === "main" && last.has(tid) && stack.length) {
      const [oldCpu, oldWall] = last.get(tid),
        duration = Math.max(0, wall - oldWall),
        cpuDuration = Math.max(0, cpu - oldCpu);
      add(leaf, name(stack.at(-1)), duration);
      add(cpuLeaf, name(stack.at(-1)), cpuDuration);
      for (const method of new Set(stack)) {
        add(inclusive, name(method), duration);
        add(cpuInclusive, name(method), cpuDuration);
      }
      const edges = new Map();
      stack.forEach((method, index) => {
        const caller = name(method),
          child = index + 1 < stack.length ? name(stack[index + 1]) : "[self]";
        if (caller.startsWith("com.cloudgeek.spool.android."))
          edges.set(JSON.stringify([caller, child]), [caller, child]);
      });
      for (const [caller, child] of edges.values()) {
        if (!branches.has(caller)) branches.set(caller, new Map());
        add(branches.get(caller), child, cpuDuration);
      }
    }
    last.set(tid, [cpu, wall]);
    const method = (encoded & ~3) >>> 0,
      action = encoded & 3;
    if (action === 0) stack.push(method);
    else if (stack.includes(method))
      while (stack.length && stack.pop() !== method) {}
  }
  const sorted = (map) => [...map].sort((a, b) => b[1] - a[1]),
    ms = (value) => Math.round(value) / 1000;
  const object = (map, predicate = () => true, count = Infinity) =>
    Object.fromEntries(
      sorted(map)
        .filter(([key]) => predicate(key))
        .slice(0, count)
        .map(([key, value]) => [key, ms(value)]),
    );
  const app = (key) => key.startsWith("com.cloudgeek.spool.android."),
    platform = (key) =>
      [
        "androidx.recyclerview.",
        "android.widget.TextView.",
        "android.text.",
        "android.view.ViewRootImpl.",
        "android.view.ThreadedRenderer.",
      ].some((prefix) => key.startsWith(prefix));
  return {
    trace,
    clock: "dual",
    overflow: header.includes("data-file-overflow=true"),
    sampledMainWallMs: ms([...leaf.values()].reduce((a, b) => a + b, 0)),
    sampledMainCpuMs: ms([...cpuLeaf.values()].reduce((a, b) => a + b, 0)),
    leafWallMs: object(leaf, undefined, 25),
    leafCpuMs: object(cpuLeaf, undefined, 25),
    platformInclusiveCpuMs: object(cpuInclusive, platform),
    appInclusiveWallMs: object(inclusive, app),
    appInclusiveCpuMs: object(cpuInclusive, app),
    appMethodChildrenCpuMs: Object.fromEntries(
      sorted(cpuInclusive)
        .filter(([key]) => branches.has(key))
        .map(([key]) => [key, object(branches.get(key), undefined, 12)]),
    ),
  };
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { values, positionals } = cli(
    { output: { type: "string" } },
    { positionals: true },
  );
  assert.equal(positionals.length, 1, "Pass one trace file");
  const text =
    JSON.stringify(
      summarize(readFileSync(positionals[0]), positionals[0]),
      null,
      2,
    ) + "\n";
  if (values.output) writeFileSync(values.output, text);
  process.stdout.write(text);
}
