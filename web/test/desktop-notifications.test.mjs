import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";

test("desktop settings wire opt-in and test controls with an unsupported fallback", async () => {
  const source = await readFile(
    new URL("../src/settings-pages.tsx", import.meta.url),
    "utf8",
  );
  const appearance = source.slice(
    source.indexOf("export function AppearancePage"),
    source.indexOf("export function AboutPage"),
  );
  assert.match(appearance, /Card title=\{t\("System notifications"\)\}/);
  assert.match(appearance, /value=\{data\.notifications_enabled === true\}/);
  assert.match(appearance, /disabled=\{!data\.notifications_supported\}/);
  assert.match(appearance, /kind: "save-notifications", enabled/);
  assert.match(appearance, /kind: "test-notification"/);
  assert.match(
    appearance,
    /!data\.notifications_enabled \|\| !data\.notifications_supported/,
  );
  assert.match(appearance, /Spool must keep running/);
  assert.match(appearance, /skipped while the desktop window has focus/);
  assert.match(appearance, /not replayed later/);
  assert.match(appearance, /Test notifications still appear/);
  assert.match(appearance, /never message contents/);
  assert.match(appearance, /System notifications are unavailable/);
});
