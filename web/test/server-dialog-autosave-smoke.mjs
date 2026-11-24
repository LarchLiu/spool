// Production remote server editor with a synthetic native persistence boundary.
// Run through tools/run.sh; SPOOL_NODE_MODULES may point to shared Playwright.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../..", import.meta.url));
const { chromium, webkit } = createRequire(path.join(
  process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__server_dialog__.cjs",
))("playwright");
const output = path.join(root, "build/artifacts/server-dialog-autosave");
await mkdir(output, { recursive: true });
const cleanup = [];
const server = await createTestViteServer({ after: (close) => cleanup.push(close) }, {
  root: path.join(root, "web"), plugins: [react()], logLevel: "error", configFile: false,
  optimizeDeps: { entries: ["servers.html"] },
  server: { host: "127.0.0.1", port: 0, hmr: false, watch: null },
});
try {
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, browserType, options] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    if (process.env.SPOOL_SMOKE_ENGINE && process.env.SPOOL_SMOKE_ENGINE !== engine) continue;
    const browser = await browserType.launch({ headless: true, ...options });
    const context = await browser.newContext({ viewport: { width: 560, height: 660 }, locale: "en-US", colorScheme: "light" });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    let availableHeight = 860;
    let dialogWidth = 560;
    await page.exposeFunction("fixtureResizeDialog", async (height) => {
      await page.setViewportSize({ width: dialogWidth, height: Math.min(height, 660, availableHeight - 40) });
    });
    try {
      await page.addInitScript(() => {
        const profile = { id: "", name: "", server_name: "", address: "", mode: "direct", direct: "", tailscale: "", relay: "", enabled: false, automatic: true };
        window.spoolServerDialog = window.fixtureServer = { server: profile, status: "disconnected", selected: false, active: false };
        window.fixtureCalls = [];
        window.fixtureCreated = 0;
        window.mygo = {
          platform: "darwin", locale: "en", on: () => () => {},
          async call(method, ...args) {
            window.fixtureCalls.push([method, ...structuredClone(args)]);
            if (method === "DesktopServers.Ready") { await window.fixtureResizeDialog(args[1]); return true; }
            if (method === "DesktopServers.Snapshot") return structuredClone(window.fixtureServer);
            if (method === "DesktopServers.Close") return true;
            if (method === "DesktopServers.Save") {
              if (window.fixtureRejectSave) throw new Error("Fixture save failed.");
              const next = structuredClone(args[0]);
              if (next.id !== window.fixtureServer.server.id) throw new Error("Wrong server identity");
              if (next.enabled && !next[next.mode]) throw new Error("Configure the selected connection before connecting.");
              if (!next.id) next.id = `server-${++window.fixtureCreated}`;
              next.name = next.name.trim();
              window.fixtureServer.server = next;
              if (args[1]) { window.fixtureServer.status = "connecting"; window.fixtureServer.active = true; }
              const result = structuredClone(window.fixtureServer);
              if (window.fixtureHoldSave) {
                window.fixtureHoldSave = false;
                await new Promise((resolve) => { window.fixtureFinishSave = resolve; });
              }
              return result;
            }
            throw new Error(`Unexpected method: ${method}`);
          },
        };
      });
      await page.goto(origin + "/servers.html");
      const name = page.getByRole("textbox", { name: "Display name (optional)", exact: true });
      await name.waitFor();
      await page.waitForFunction(() => window.fixtureCalls.some(([method]) => method === "DesktopServers.Ready"));
      assert.equal(await page.getByRole("button", { name: "Save settings", exact: true }).count(), 0);
      assert.equal(await page.getByRole("region", { name: "Pending changes", exact: true }).count(), 0);
      assert.equal(await page.evaluate(() => window.fixtureCalls.filter(([method]) => method === "DesktopServers.Save").length), 0);
      await page.evaluate(() => { window.fixtureHoldSave = true; });
      await name.fill("First name");
      await name.press("Tab");
      await page.waitForFunction(() => typeof window.fixtureFinishSave === "function");
      assert.equal(await name.isEditable(), true, "saving keeps the editor usable");
      await name.fill("Latest name");
      const address = page.getByRole("textbox", { name: "Public HTTPS address", exact: true });
      await address.fill("https://fixture.example");
      await address.press("Tab");
      await page.evaluate(() => window.fixtureFinishSave());
      await page.waitForFunction(() => window.fixtureServer.server.name === "Latest name" && window.fixtureServer.server.direct === "https://fixture.example");
      assert.equal(await name.inputValue(), "Latest name");
      assert.equal(await page.evaluate(() => window.fixtureCreated), 1, "queued saves reuse a newly created server");
      await page.getByRole("switch", { name: "Allow automatic connection switching", exact: true }).click();
      await page.waitForFunction(() => !window.fixtureServer.server.automatic);
      await page.getByRole("button", { name: "Connect", exact: true }).click();
      await page.getByRole("button", { name: "Disconnect", exact: true }).waitFor();

      await page.evaluate(() => { window.fixtureRejectSave = true; });
      await name.fill("Retry this edit");
      await name.press("Enter");
      await page.getByRole("alert").getByText("Fixture save failed.", { exact: true }).waitFor();
      assert.equal(await name.inputValue(), "Retry this edit");
      await page.evaluate(() => { window.fixtureRejectSave = false; });
      await name.press("Enter");
      await page.waitForFunction(() => window.fixtureServer.server.name === "Retry this edit");
      await page.getByRole("alert").waitFor({ state: "hidden" });
      await address.fill("https://");
      await address.press("Enter");
      assert.equal(await address.evaluate((input) => input.checkValidity()), false);
      assert.equal(await page.evaluate(() => window.fixtureServer.server.direct), "https://fixture.example");
      await name.fill("Name beside invalid address");
      await name.press("Enter");
      await page.waitForFunction(() => window.fixtureServer.server.name === "Name beside invalid address");
      assert.equal(await address.inputValue(), "https://", "another save preserves incomplete input");
      await address.fill("https://changed.example");
      await page.getByRole("button", { name: "Close", exact: true }).click();
      await page.waitForFunction(() => window.fixtureCalls.some(([method]) => method === "DesktopServers.Close"));
      assert.equal(await page.evaluate(() => window.fixtureServer.server.direct), "https://changed.example", "closing waits for the final edit to save");

      await page.waitForFunction(() => innerHeight === Math.ceil(document.querySelector(".server-dialog-content").getBoundingClientRect().height));
      const footerGap = await page.locator(".server-dialog-footer").evaluate((footer) => innerHeight - footer.getBoundingClientRect().bottom);
      assert.ok(footerGap >= 25 && footerGap < 26, "only bottom padding follows the form");
      await page.screenshot({ path: path.join(output, `${engine}-server.png`) });
      const compact = page.viewportSize().height;
      await page.getByRole("button", { name: "Delete server", exact: true }).click();
      await page.waitForFunction((compact) => innerHeight > compact, compact);
      availableHeight = 380;
      await page.setViewportSize({ width: 560, height: 340 });
      const confirm = page.getByRole("alert").getByRole("button", { name: "Delete server", exact: true });
      await confirm.scrollIntoViewIfNeeded();
      const box = await confirm.boundingBox();
      assert.ok(box.y >= 0 && box.y + box.height <= 340, "short dialogs scroll to their final action");
      availableHeight = 860;
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await page.waitForFunction((compact) => innerHeight === compact, compact);

      await page.evaluate(() => { window.fixtureHoldSave = true; window.fixtureFinishSave = undefined; });
      await name.fill("Save for an old server");
      await name.press("Tab");
      await page.waitForFunction(() => typeof window.fixtureFinishSave === "function");
      await page.evaluate(() => {
        const next = structuredClone(window.fixtureServer);
        next.server.id = "reopened";
        next.server.name = "Reopened server";
        window.fixtureServer = next;
        window.dispatchEvent(new CustomEvent("spool:server-dialog", { detail: next }));
        window.fixtureFinishSave();
      });
      await page.getByRole("heading", { name: "Reopened server · Settings", exact: true }).waitFor();
      assert.equal(await name.inputValue(), "Reopened server");
      for (const width of [560, 360]) {
        dialogWidth = width;
        await page.setViewportSize({ width, height: 660 });
        await page.waitForFunction(() => innerHeight === Math.ceil(document.querySelector(".server-dialog-content").getBoundingClientRect().height));
        const title = await page.getByRole("heading", { level: 1 }).boundingBox();
        const close = await page.getByRole("button", { name: "Close", exact: true }).boundingBox();
        const connection = await page.getByRole("textbox", { name: "Public HTTPS address", exact: true }).boundingBox();
        const remove = await page.getByRole("button", { name: "Delete server", exact: true }).boundingBox();
        const enabled = page.getByRole("switch", { name: "Connect to this server", exact: true });
        const enabledRow = await enabled.locator("xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' settings-row ')][1]").boundingBox();
        assert.ok(title.x + title.width <= close.x, "the title and close action never overlap");
        assert.ok(title.y + title.height < enabledRow.y, "server settings follow the heading");
        assert.equal(Math.round(enabledRow.x), Math.round(connection.x), "identity and connection settings share one left edge");
        assert.equal(Math.round(remove.x), Math.round(connection.x), "Delete server aligns with the connection fields");
        assert.equal(Math.round(remove.width), Math.round(connection.width), "Delete server spans the settings width, as on Android");
        assert.ok(remove.y > connection.y + connection.height, "Delete server follows the connection controls");
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "the settings fit narrow dialogs");
        assert.ok(await page.getByRole("tablist", { name: "Connection mode", exact: true }).locator(".segmented-tab-label").evaluateAll((labels) => labels.every((label) => label.scrollWidth <= label.clientWidth)), "connection names remain readable in narrow dialogs");
        for (const theme of ["light", "dark"]) {
          await page.emulateMedia({ colorScheme: theme });
          await page.waitForFunction((theme) => document.documentElement.dataset.theme === theme, theme);
          await page.screenshot({ path: path.join(output, `${engine}-server-${width}-${theme}.png`), animations: "disabled" });
        }
      }
      await page.evaluate(() => {
        const next = structuredClone(window.fixtureServer);
        Object.assign(next.server, { id: "trusted-relay", name: "Trusted Relay", mode: "relay", direct: "", tailscale: "", relay: "spool-pair:fixture", federation_relay: true });
        next.active = false; next.status = "disconnected";
        window.fixtureServer = next;
        window.dispatchEvent(new CustomEvent("spool:server-dialog", { detail: next }));
      });
      await page.getByRole("heading", { name: "Trusted Relay · Settings", exact: true }).waitFor();
      const modes = page.getByRole("tablist", { name: "Connection mode", exact: true });
      assert.equal(await modes.getByRole("tab").count(), 1);
      assert.equal(await modes.getByRole("tab", { name: /Spool Relay/ }).getAttribute("aria-selected"), "true");
      assert.equal(await page.locator('[data-server-field="relay"]').isEnabled(), false);
      assert.equal(await page.getByRole("button", { name: "Scan pairing QR code", exact: true }).count(), 0);
      await name.fill("Renamed trusted Relay"); await name.press("Enter");
      await page.waitForFunction(() => window.fixtureServer.server.name === "Renamed trusted Relay");
      assert.equal(await page.evaluate(() => window.fixtureServer.server.federation_relay), true);
      assert.deepEqual(errors, []);
      console.log(`${engine}: remote server autosave, retry, isolation, Android layout and sizing checks passed`);
    } catch (error) {
      console.error(engine, await page.evaluate(() => ({ server: window.fixtureServer, calls: window.fixtureCalls.slice(-5) })));
      await page.screenshot({ path: path.join(output, `${engine}-failure.png`) });
      throw error;
    } finally {
      await context.close();
      await browser.close();
    }
  }
} finally {
  for (const close of cleanup.reverse()) await close();
}
