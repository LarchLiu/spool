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

test("Go timing CLI preserves flags, streaming output, exit status and summary colors", { skip: process.platform === "win32" }, (t) => {
  const root = mkdtempSync(join(tmpdir(), "spool-go-timing-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  mkdirSync(bin);
  const fakeGo = join(bin, "go");
  writeFileSync(fakeGo, `#!/bin/sh
printf '%s\\n' "$*"
printf '%s\\n' '{"Action":"output","Package":"fixture","Test":"TestResult","Output":"test context\\n"}' "{\\"Action\\":\\"$RESULT\\",\\"Package\\":\\"fixture\\",\\"Test\\":\\"TestResult\\",\\"Elapsed\\":0.2}" "{\\"Action\\":\\"$RESULT\\",\\"Package\\":\\"fixture\\",\\"Elapsed\\":0.3}"
exit "$STATUS"
`, { mode: 0o755 });
  for (const status of [0, 7]) {
    for (const [forceColor, noColor, colored] of [["1", undefined, true], ["1", "1", false], [undefined, undefined, false]]) {
      const result = spawnSync(process.execPath, [fileURLToPath(new URL("./go-test.mjs", import.meta.url)), "-race", "-run", "TestResult", "./fixture"], {
        env: { ...process.env, PATH: bin + ":" + process.env.PATH, STATUS: String(status), RESULT: status === 0 ? "pass" : "fail", FORCE_COLOR: forceColor, NO_COLOR: noColor },
        encoding: "utf8", timeout: 10000,
      });
      assert.equal(result.status, status, result.stderr);
      assert.match(result.stdout, /test -json -race -run TestResult \.\/fixture/);
      const summary = status === 0 ? result.stdout : result.stderr;
      assert.match(summary, new RegExp(`\\[go-test\\].*exit ${status}`));
      assert.match(summary, /0\.300s fixture \((pass|fail)\)/);
      assert.match(summary, /0\.200s fixture\/TestResult/);
      if (status === 0) assert.equal(result.stderr, "");
      else assert.match(result.stdout, /test context/);
      if (colored) {
        const color = status === 0 ? "\x1b[32m" : "\x1b[31m";
        assert.ok(summary.includes(color + "[go-test]"));
        assert.ok(summary.includes(color + "  package"));
        assert.ok(summary.includes(color + "  test"));
      } else assert.doesNotMatch(summary, /\x1b\[/);
    }
  }
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
