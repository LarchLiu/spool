import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

const compiled = ts.transpileModule(
  readFileSync(
    new URL("../src/use-session-notifications.ts", import.meta.url),
    "utf8",
  ),
  {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.CommonJS,
    },
  },
).outputText;
const module = { exports: {} };
vm.runInNewContext(compiled, {
  module,
  exports: module.exports,
  require: () => ({}),
});
const { receiveSessionNotifications } = module.exports;
const user = "user-12345678";
const note = (chat, kind, owner = user) => ({ chat, kind, user: owner });

test("session states keep the latest status per chat and isolate recipient accounts", () => {
  const previous = { first: "completed" };
  const next = receiveSessionNotifications(
    previous,
    [
      note("first", "failed"),
      note("second", "approval"),
      note("first", "completed", "another-user-123456"),
    ],
    user,
  );
  assert.deepEqual({ ...next }, { first: "failed", second: "approval" });
  assert.deepEqual(previous, { first: "completed" });
});

test("foreground current-session events clear its indicator while other and background sessions update", () => {
  const previous = { current: "approval", other: "completed" };
  const pending = [note("current", "completed"), note("other", "failed")];
  assert.deepEqual(
    { ...receiveSessionNotifications(previous, pending, user, "current") },
    { other: "failed" },
  );
  assert.deepEqual(
    { ...receiveSessionNotifications(previous, pending, user) },
    { current: "completed", other: "failed" },
  );
});
