#!/usr/bin/env bun
// Stream Go test results and collect timings in the same execution.
import { spawn } from "node:child_process";
import { constants } from "node:os";
import { createInterface } from "node:readline";
import { printStatus } from "./status.mjs";

export class GoTestReporter {
  constructor({ verbose = false, write = (text) => process.stdout.write(text) } = {}) {
    this.verbose = verbose;
    this.write = write;
    this.packages = new Map();
    this.tests = [];
    this.output = new Map();
    this.cached = new Set();
  }

  accept(event) {
    const { Package: pkg, Test: test, Action: action, Output: output, Elapsed: elapsed = 0 } = event;
    const key = `${pkg}/${test}`;
    if (typeof output === "string") {
      if (!test || this.verbose) this.write(output);
      else {
        // Keep useful failure context without retaining successful histories.
        this.output.set(key, ((this.output.get(key) ?? "") + output).slice(-32768));
      }
      if (!test && output.includes("(cached)")) this.cached.add(pkg);
    }
    if (!["pass", "fail", "skip"].includes(action)) return;
    if (!test) {
      this.packages.set(pkg, { name: pkg, result: action, seconds: elapsed });
      return;
    }
    if (action === "fail" && !this.verbose) this.write(this.output.get(key) ?? "");
    this.output.delete(key);
    // Parent test elapsed time already includes its subtests.
    if (!test.includes("/")) this.tests.push({ package: pkg, name: test, result: action, seconds: elapsed });
  }

  summary() {
    const slowest = (items) => items.filter((item) => item.result !== "skip" && !this.cached.has(item.package ?? item.name))
      .sort((a, b) => b.seconds - a.seconds).slice(0, 5);
    return {
      packages: [...this.packages.values()],
      cachedPackages: [...this.cached],
      slowestPackages: slowest([...this.packages.values()]),
      slowestTests: slowest(this.tests),
    };
  }
}

if (import.meta.main) {
  const args = process.argv.slice(2);
  const reporter = new GoTestReporter({ verbose: args.some((arg) => arg === "-v" || arg === "-v=true") });
  const started = performance.now();
  const child = spawn("go", ["test", "-json", ...args], { stdio: ["inherit", "pipe", "inherit"] });
  const lines = createInterface({ input: child.stdout });
  const completed = new Promise((resolve) => {
    child.once("error", (error) => {
      console.error(error.message);
      lines.close();
      child.stdout.destroy();
      resolve(1);
    });
    child.once("close", (code, signal) => resolve(code ?? 128 + constants.signals[signal]));
  });
  for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
  try {
    for await (const line of lines) {
      let event;
      try { event = JSON.parse(line); } catch { process.stdout.write(line + "\n"); continue; }
      reporter.accept(event);
    }
    process.exitCode = await completed;
    const summary = reporter.summary();
    printStatus(`[go-test] ${((performance.now() - started) / 1000).toFixed(2)}s; ${summary.packages.length} packages; ${summary.cachedPackages.length} cached; exit ${process.exitCode}`, process.exitCode === 0);
    for (const pkg of summary.slowestPackages) printStatus(`  package ${pkg.seconds.toFixed(3)}s ${pkg.name} (${pkg.result})`, pkg.result === "pass");
    for (const test of summary.slowestTests) printStatus(`  test    ${test.seconds.toFixed(3)}s ${test.package}/${test.name} (${test.result})`, test.result === "pass");
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
