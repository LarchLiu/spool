// Production App with synthetic server/USB boundaries; run through tools/run.sh.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__connections__.cjs"));
const { chromium } = require("playwright");
const output = path.join(root, "build/artifacts/connections");
const cleanup = [];
const server = await createTestViteServer({ after: (fn) => cleanup.push(fn) }, {
  configFile: false, root: path.join(root, "web"), plugins: [react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ channel: "chrome", headless: true });
  for (const [width, supportsUSB, desktopPlatform] of [
    [1280, true], [390, true], [320, true],
    [1280, false], [390, false], [320, false],
    [1280, true, "darwin"], [1280, true, "win32"], [1280, true, "linux"],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height: 820 }, serviceWorkers: "block",
      hasTouch: width < 600,
    });
    try {
      const page = await context.newPage();
      page.setDefaultTimeout(8000);
      const errors = [], writes = [];
      page.on("pageerror", (error) => errors.push(String(error)));
      await page.addInitScript(({ supportsUSB, desktopPlatform }) => {
        localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
        if (desktopPlatform) {
          window.spoolDesktop = true;
          window.mygo = { platform: desktopPlatform };
        }
        const alternate = {
          alternateSetting: 0, interfaceClass: 255, interfaceSubclass: 66, interfaceProtocol: 1,
          endpoints: [
            { endpointNumber: 1, direction: "in", type: "bulk", packetSize: 512 },
            { endpointNumber: 2, direction: "out", type: "bulk", packetSize: 512 },
          ],
        };
        const configuration = { configurationValue: 1, interfaces: [{ interfaceNumber: 0, alternate, alternates: [alternate] }] };
        let rejectRead;
        const device = {
          vendorId: 0x18d1, productId: 0x4ee7, serialNumber: "fixture", productName: "Fixture phone",
          configuration, configurations: [configuration],
          async open() {}, async selectConfiguration() {}, async claimInterface() {},
          async selectAlternateInterface() {}, async releaseInterface() {},
          async close() { rejectRead?.(new DOMException("Closed", "AbortError")); },
          transferIn() { return new Promise((resolve, reject) => { rejectRead = reject; }); },
          async transferOut() { return { status: "ok" }; },
        };
        const usb = new EventTarget();
        usb.requestDevice = async () => device;
        usb.getDevices = async () => [device];
        Object.defineProperty(navigator, "usb", { configurable: true, value: supportsUSB ? usb : undefined });
        window.WebSocket = class extends EventTarget {
          readyState = 0;
          bufferedAmount = 0;
          constructor(url) {
            super();
            if (String(url).includes("/adb/bridge")) {
              window.fixtureADB = this;
              setTimeout(() => { this.readyState = 1; this.onopen?.({}); }, 0);
            }
          }
          send() {}
          close() { this.readyState = 3; }
        };
      }, { supportsUSB, desktopPlatform });
      let authorized = true;
      let adbEnabled = true;
      let adbReads = 0;
      let desktopReads = 0;
      // The native shell consumes this navigation without leaving the chat.
      await page.route("**/__desktop/session-changed", (route) => route.fulfill({ status: 204 }));
      await page.route("**/api/**", async (route) => {
        const request = route.request(), endpoint = new URL(request.url()).pathname;
        const json = (value) => route.fulfill({ contentType: "application/json", body: JSON.stringify(value) });
        if (request.method() === "POST") writes.push(endpoint);
        if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
        if (endpoint === "/api/navigation") return json({ projects: [], chats: [], sidebar: { revision: 1, pinned: [] } });
        if (endpoint === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [] } }]);
        if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
        if (endpoint === "/api/desktop") {
          desktopReads++;
          return json({ available: authorized });
        }
        if (endpoint === "/api/adb") {
          adbReads++;
          return json(!authorized ? { available: false, reason: "not_authorized" }
            : adbEnabled ? { available: true } : { available: false, reason: "disabled" });
        }
        if (endpoint === "/api/desktop/connect") return json({ token: "fixture-view" });
        if (endpoint === "/api/desktop/view/") return route.fulfill({ contentType: "text/html", body: "<!doctype html><p>Fixture desktop</p>" });
        return json({});
      });
      await page.goto(origin);
      if (width < 600) await page.getByRole("button", { name: "Open sidebar", exact: true }).click();
      const footer = page.locator(".side-foot");
      const remote = footer.getByRole("button", { name: "Remote connection", exact: true });
      const adb = footer.getByRole("button", { name: "ADB bridge", exact: true });
      await footer.waitFor();
      assert.equal(await footer.getByRole("button", { name: "Sign out", exact: true }).getAttribute("title"), "Sign out", "Sign out has a native name tooltip");
      if (desktopPlatform) {
        await footer.getByRole("link", { name: "Settings", exact: true }).waitFor();
        assert.equal(await footer.getByRole("link", { name: "Settings", exact: true }).locator(".lucide-settings2").count(), 1);
        assert.equal(await remote.count(), 0, "Desktop hides the remote connection entry");
        assert.equal(await adb.count(), 0, "Desktop hides the ADB bridge entry even with WebUSB");
        assert.equal(desktopReads, 0, "Desktop does not poll remote desktop availability");
        assert.equal(adbReads, 0, "Desktop does not poll ADB availability");
        assert.equal(await footer.locator(".connection-status-dot").count(), 0);
        assert.deepEqual(errors, []);
        console.log(`PASS: ${desktopPlatform} desktop hides both connection entries and skips availability polling`);
        continue;
      }
      assert.equal(await footer.getByRole("button", { name: "Appearance and language", exact: true }).locator(".lucide-palette").count(), 1);
      await remote.waitFor();
      if (!supportsUSB) {
        assert.equal(await adb.count(), 0, "Browsers without WebUSB hide the ADB entry");
        assert.equal(adbReads, 0, "Browsers without WebUSB do not poll ADB availability");
        assert.deepEqual(await footer.locator("button.icon").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label"))),
          ["Remote connection", "Appearance and language", "Sign out"]);
        assert.deepEqual(errors, []);
        console.log(`PASS: connections at ${width}px without WebUSB; ADB entry hidden and no ADB polling`);
        continue;
      }
      await adb.waitFor();
      assert.deepEqual(await footer.locator("button.icon").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label"))),
        ["Remote connection", "ADB bridge", "Appearance and language", "Sign out"]);
      assert.equal(await footer.locator(".connection-status-dot").count(), 0);
      assert.equal(await remote.getAttribute("title"), "Remote connection");
      assert.equal(await adb.getAttribute("title"), "ADB bridge");
      if (width >= 600) {
        await remote.hover();
        assert.equal(await page.getByRole("dialog", { name: "Remote connection", exact: true }).count(), 0, "Hover only shows the native title tooltip");
        await adb.hover();
        assert.equal(await page.getByRole("dialog", { name: "ADB bridge", exact: true }).count(), 0);
        await page.mouse.move(800, 100);
      }
      await remote.focus();
      assert.equal(await page.getByRole("dialog", { name: "Remote connection", exact: true }).count(), 0, "Focusing the icon does not open the popup");
      await page.keyboard.press("Enter");
      const remoteDialog = page.getByRole("dialog", { name: "Remote connection", exact: true });
      await remoteDialog.waitFor();
      assert.equal(await remoteDialog.evaluate((element) => element.matches(":modal")), false, "Connection opens as an anchored popup");
      const remoteBounds = await remoteDialog.boundingBox();
      const remoteAnchor = await remote.boundingBox();
      assert.ok(Math.abs(remoteBounds.y + remoteBounds.height - remoteAnchor.y + 8) < 1, "Popup sits just above its icon");
      await remote.click();
      await remoteDialog.waitFor({ state: "hidden" });
      assert.equal(await remote.getAttribute("aria-expanded"), "false", "Clicking the icon again closes its popup");
      await remote.focus();
      await page.keyboard.press("Space");
      await remoteDialog.waitFor();
      await page.keyboard.press("Escape");
      assert.equal(await remote.getAttribute("aria-expanded"), "false");
      assert.ok(await remote.evaluate((element) => element === document.activeElement), "Escape restores focus to the icon");
      assert.ok(await remote.isVisible(), "Escape leaves the mobile sidebar open");
      await remote.click();
      await page.getByRole("button", { name: "Connect to desktop", exact: true }).click();
      await page.locator(".remote-desktop-frame").waitFor();
      assert.equal(await remote.locator(".connection-status-dot[data-state=connected]").count(), 1);
      await page.getByRole("button", { name: "Disconnect desktop", exact: true }).click();
      assert.equal(await remote.locator(".connection-status-dot").count(), 0);
      assert.ok(writes.includes("/api/desktop/connect"));

      await adb.click();
      const panel = page.getByRole("dialog", { name: "ADB bridge", exact: true });
      await panel.waitFor();
      assert.equal(await panel.evaluate((element) => element.matches(":modal")), false);
      await page.mouse.click(4, 4);
      await panel.waitFor({ state: "hidden" });
      await adb.click();
      await panel.waitFor();
      const bounds = await panel.boundingBox();
      const adbAnchor = await adb.boundingBox();
      assert.ok(Math.abs(bounds.y + bounds.height - adbAnchor.y + 8) < 1, "ADB popup sits just above its icon");
      assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width && bounds.y >= 0 && bounds.y + bounds.height <= 820, "Panel fits the viewport");
      await page.getByRole("button", { name: "Share a phone over USB", exact: true }).click();
      await page.locator('[data-adb-share="connecting"]').waitFor();
      assert.equal(await adb.locator(".connection-status-dot[data-state=pending]").count(), 1);
      await page.waitForFunction(() => window.fixtureADB?.readyState === 1);
      await page.evaluate(() => {
        const message = (value) => window.fixtureADB.onmessage({ data: JSON.stringify(value) });
        message({ type: "ready", serial: "127.0.0.1:41000" });
        message({ type: "state", state: "online" });
      });
      await page.locator('[data-adb-share="online"]').waitFor();
      assert.equal(await adb.locator(".connection-status-dot[data-state=connected]").count(), 1);
      await page.screenshot({ path: path.join(output, `sharing-${width}.png`) });
      await page.keyboard.press("Escape");
      await panel.waitFor({ state: "hidden" });
      assert.equal(await adb.locator(".connection-status-dot").count(), 1, "Closing the panel keeps sharing alive");
      await adb.click();
      await page.getByRole("button", { name: "Stop sharing", exact: true }).click();
      assert.equal(await adb.locator(".connection-status-dot").count(), 0);
      await page.keyboard.press("Escape");
      adbEnabled = false;
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      await adb.waitFor({ state: "hidden" });
      assert.ok(await remote.isVisible(), "Disabling ADB leaves remote desktop available");
      adbEnabled = true;
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      await adb.waitFor();
      await footer.getByRole("button", { name: "Appearance and language", exact: true }).click();
      const settings = page.getByRole("dialog", { name: "Appearance and language", exact: true });
      assert.equal(await settings.getByText("ADB bridge", { exact: true }).count(), 0);
      assert.equal(await settings.getByText("Remote connection", { exact: true }).count(), 0);
      const repository = settings.getByRole("link", { name: "Open GitHub repository", exact: true });
      assert.equal(await repository.getAttribute("href"), "https://github.com/LarchLiu/spool");
      assert.equal(await repository.getAttribute("target"), "_blank");
      const versionRow = settings.locator(".appearance-version");
      const versionBounds = await versionRow.locator("span").boundingBox();
      const repositoryBounds = await repository.boundingBox();
      assert.ok(repositoryBounds.x > versionBounds.x + versionBounds.width, "GitHub follows the version on the right");
      assert.ok(Math.abs(repositoryBounds.y + repositoryBounds.height / 2 - versionBounds.y - versionBounds.height / 2) < 2, "Version and GitHub align on the same row");
      assert.ok(repositoryBounds.x + repositoryBounds.width <= width, "GitHub link stays within the viewport");
      await settings.screenshot({ path: path.join(output, `settings-${width}.png`) });
      await context.route("https://github.com/LarchLiu/spool", (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Repository fixture</title>" }));
      const popup = page.waitForEvent("popup");
      await repository.click();
      const repositoryPage = await popup;
      await repositoryPage.waitForLoadState("domcontentloaded");
      assert.equal(repositoryPage.url(), "https://github.com/LarchLiu/spool");
      await repositoryPage.close();
      await settings.getByRole("button", { name: "Close settings", exact: true }).click();
      authorized = false;
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      await adb.waitFor({ state: "hidden" });
      await remote.waitFor({ state: "hidden" });
      assert.deepEqual(errors, []);
      console.log(`PASS: connections at ${width}px; title tooltips, click/keyboard popups, anchoring, dismissal, focus, status, sharing lifetime and settings separation`);
    } finally { await context.close(); }
  }
} finally {
  await browser?.close();
  for (const dispose of cleanup.reverse()) await dispose();
}
