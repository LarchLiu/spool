// Production workspace with synthetic native/API boundaries. Run via tools/run.sh.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer, mockDesktopNavigation } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../..", import.meta.url));
const { chromium, webkit } = createRequire(path.join(
  process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__federation__.cjs",
))("playwright");
const engines = [
  ["chromium", chromium, { channel: "chrome" }],
  ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
].filter(([engine]) => !process.env.SPOOL_SMOKE_ENGINE || process.env.SPOOL_SMOKE_ENGINE === engine);
assert.ok(engines.length, "SPOOL_SMOKE_ENGINE must be chromium or webkit");
const cleanup = [];
const navigation = mockDesktopNavigation();
const load = navigation.load;
navigation.load = function (id) {
  const source = load.call(this, id);
  return source && source + `
export function requestDesktopFederationLogin(source, target) {
  window.fixtureLogins.push({source, target});
  window.spoolServers = {...window.spoolServers, federation_pending: {source, target, state: 'pending'}};
  window.dispatchEvent(new Event('spool:servers-changed'));
}`;
};
const server = await createTestViteServer({ after: (fn) => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [react(), navigation],
  server: { host: "127.0.0.1", port: 0, strictPort: true },
});
let browser;
try {
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  const output = path.join(root, "build/artifacts/desktop-federation");
  await mkdir(output, { recursive: true });
  for (const [engine, type, options] of engines) {
    browser = await type.launch({ ...options, headless: true });
    const page = await browser.newPage({ viewport: { width: 1280, height: 860 }, serviceWorkers: "block" });
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    await page.addInitScript(() => {
      window.spoolDesktop = true;
      window.fixtureLogins = [];
      window.spoolServers = {
        selected: "trusted",
        entries: [
          { id: "home", name: "Home", local: false, status: "connected", federation_targets: [{ id: "a".repeat(52), name: "Lab" }] },
          { id: "trusted", name: "Lab", local: false, status: "login", federation_source: "home", federation_target: "a".repeat(52) },
          { id: "ordinary", name: "Ordinary", local: false, status: "login" },
        ],
      };
    });
    await page.route("**/api/**", (route) => {
      const endpoint = new URL(route.request().url()).pathname;
      return route.fulfill({
        status: endpoint === "/api/me" ? 401 : 200,
        contentType: "application/json",
        body: JSON.stringify(endpoint === "/api/version"
          ? { version: "fixture", server_name: "Lab" } : { error: "Sign in required" }),
      });
    });
    await page.goto(origin);
    const footer = page.locator(".side-foot");
    const welcome = page.locator(".desktop-server-welcome > button");
    const tabs = page.locator(".desktop-server-tabs");
    for (const entry of [footer.getByRole("button", { name: "Sign in", exact: true }), welcome]) {
      await entry.click();
      await footer.getByRole("button", { name: "Connecting…", exact: true }).waitFor();
      assert.equal(await footer.getByRole("button", { name: "Connecting…", exact: true }).isDisabled(), true);
      assert.equal(await welcome.isDisabled(), true);
      assert.equal(await tabs.getByRole("button", { name: "Home → Signing in to Lab…", exact: true }).isDisabled(), true);
      assert.equal(await page.getByRole("button", { name: "Sign in", exact: true }).count(), 0);
      assert.equal(await page.locator('input[name="password"]').count(), 0);
      await page.evaluate(() => {
        document.querySelector(".desktop-server-welcome > button").click();
        window.spoolServers = { ...window.spoolServers, selected: "ordinary" };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await footer.getByRole("button", { name: "Sign in", exact: true }).waitFor();
      assert.equal(await tabs.getByRole("button", { name: "Home → Signing in to Lab…", exact: true }).isDisabled(), true);
      await page.evaluate(() => {
        window.spoolServers = { ...window.spoolServers, selected: "trusted" };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await footer.getByRole("button", { name: "Connecting…", exact: true }).waitFor();
      await page.screenshot({ path: path.join(output, `${engine}-signing-in.png`) });
      await page.evaluate(() => {
        window.spoolServers = { ...window.spoolServers, federation_pending: undefined };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await footer.getByRole("button", { name: "Sign in", exact: true }).waitFor();
    }
    assert.deepEqual(await page.evaluate(() => window.fixtureLogins), [
      { source: "home", target: "a".repeat(52) },
      { source: "home", target: "a".repeat(52) },
    ]);
    await page.evaluate(() => {
      window.spoolServers = { ...window.spoolServers, selected: "ordinary" };
      window.dispatchEvent(new Event("spool:servers-changed"));
    });
    await footer.getByRole("button", { name: "Sign in", exact: true }).click();
    await page.locator('input[name="password"]').waitFor();
    assert.deepEqual(errors, []);
    console.log(`${engine}: trusted actions sign in directly, pending controls reject repeats across pages, ordinary login preserved`);
    await browser.close();
    browser = null;
  }
} finally {
  await browser?.close();
  for (const dispose of cleanup.reverse()) await dispose();
}
