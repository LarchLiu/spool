import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { GoTestReporter } from "./go-test.mjs";

test("Go timing reports rank parent tests once and distinguish cached results", () => {
  let output = "";
  const reporter = new GoTestReporter({ write: (text) => { output += text; } });
  for (const event of [
    { Action: "pass", Package: "slow", Test: "TestParent/child", Elapsed: 4 },
    { Action: "pass", Package: "slow", Test: "TestParent", Elapsed: 5 },
    { Action: "skip", Package: "slow", Test: "TestBrowser", Elapsed: 0 },
    { Action: "pass", Package: "slow", Elapsed: 6 },
    { Action: "pass", Package: "cached", Test: "TestOld", Elapsed: 10 },
    { Action: "output", Package: "cached", Output: "ok cached (cached)\n" },
    { Action: "pass", Package: "cached", Elapsed: 0 },
  ]) reporter.accept(event);
  const summary = reporter.summary();
  assert.deepEqual(summary.cachedPackages, ["cached"]);
  assert.deepEqual(summary.slowestPackages.map((p) => [p.name, p.seconds]), [["slow", 6]]);
  assert.deepEqual(summary.slowestTests.map((t) => [t.name, t.seconds]), [["TestParent", 5]]);
  assert.match(output, /cached/);
});

test("Go timing reports retain failure output and support verbose output", () => {
  for (const verbose of [false, true]) {
    let output = "";
    const reporter = new GoTestReporter({ verbose, write: (text) => { output += text; } });
    reporter.accept({ Action: "output", Package: "pkg", Test: "TestPass", Output: "success detail\n" });
    reporter.accept({ Action: "pass", Package: "pkg", Test: "TestPass", Elapsed: 0.1 });
    reporter.accept({ Action: "output", Package: "pkg", Test: "TestFail/child", Output: "useful failure\n" });
    reporter.accept({ Action: "fail", Package: "pkg", Test: "TestFail/child", Elapsed: 0.2 });
    reporter.accept({ Action: "fail", Package: "pkg", Test: "TestFail", Elapsed: 0.3 });
    assert.equal(output.includes("success detail"), verbose);
    assert.equal(output.match(/useful failure/g)?.length, 1);
    assert.equal(reporter.summary().slowestTests[0].result, "fail");
    assert.equal(reporter.output.size, 0);
  }
});

test("Go timing CLI preserves flags, streaming output and failing exit status", { skip: process.platform === "win32" }, (t) => {
  const root = mkdtempSync(join(tmpdir(), "spool-go-timing-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  mkdirSync(bin);
  const fakeGo = join(bin, "go");
  writeFileSync(fakeGo, `#!/bin/sh
printf '%s\\n' "$*" >&2
printf '%s\\n' '{"Action":"output","Package":"fixture","Test":"TestFailure","Output":"failure context\\n"}' '{"Action":"fail","Package":"fixture","Test":"TestFailure","Elapsed":0.2}' '{"Action":"output","Package":"fixture","Output":"FAIL fixture\\n"}' '{"Action":"fail","Package":"fixture","Elapsed":0.3}'
exit 7
`, { mode: 0o755 });
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./go-test.mjs", import.meta.url)), "-race", "-run", "TestFailure", "./fixture"], {
    env: { ...process.env, PATH: bin + ":" + process.env.PATH }, encoding: "utf8", timeout: 10000,
  });
  assert.equal(result.status, 7, result.stderr);
  assert.match(result.stderr, /test -json -race -run TestFailure \.\/fixture/);
  assert.match(result.stderr, /\[go-test\].*exit 7/);
  assert.match(result.stdout, /failure context/);
  assert.match(result.stderr, /0\.200s fixture\/TestFailure/);
});

test("Go timing CLI reports a missing Go tool as a failure", { skip: process.platform === "win32" }, (t) => {
  const root = mkdtempSync(join(tmpdir(), "spool-go-missing-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./go-test.mjs", import.meta.url)), "./fixture"], {
    env: { ...process.env, PATH: root }, encoding: "utf8", timeout: 10000,
  });
  assert.equal(result.status, 1, result.stderr);
  assert.match(result.stderr, /go|ENOENT/);
});
