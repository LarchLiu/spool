import { requireRunTemp } from "../../tools/test-scratch.mjs";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

requireRunTemp();
const root = resolve(import.meta.dirname, "../..");
const { chromium } = createRequire(join(process.env.SPOOL_NODE_MODULES || join(root, "web/node_modules"), "__session_images__.cjs"))("playwright");
const origin = process.env.SPOOL_IMAGE_ORIGIN;
assert.match(origin || "", /^http:\/\/127\.0\.0\.1:\d+$/);
const scratch = await mkdtemp(join(process.env.TMPDIR, "session-images-"));
const output = join(root, "build/artifacts/session-images");
const reports = [];
let context;
try {
  await mkdir(output, { recursive: true });
  for (const [surface, width] of [["web", 1280], ["desktop", 1280], ["mobile", 390]]) {
    context = await chromium.launchPersistentContext(join(scratch, surface), {
      executablePath: process.env.CHROME_BIN, headless: true, viewport: { width, height: 900 }, serviceWorkers: "block",
    });
    try {
      if (surface === "desktop") await context.addInitScript(() => { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; });
      assert.equal((await context.request.post(origin + "/api/login", { headers: { Origin: origin }, data: { username: "owner", password: "test-password-2026" } })).status(), 200);
      const page = await context.newPage();
      await page.route("**/__desktop/view?*", route => route.fulfill({ status: 204 }));
      const errors = [];
      page.on("pageerror", error => errors.push(error.message));
      await page.goto(origin + "/?chat=" + process.env.SPOOL_IMAGE_CHAT);
      const card = page.locator(".user-row .attachment-open").first();
      await card.waitFor();
      await card.locator("img").evaluate(async image => { if (!image.complete) await new Promise(resolve => image.addEventListener("load", resolve, { once: true })); assertImage(image); function assertImage(image) { if (!image.naturalWidth) throw new Error("Image did not load"); } });
      const source = await card.locator("img").getAttribute("src");
      assert.ok(source.includes("image=1"));
      await card.click();
      const dialog = page.getByRole("dialog");
      await dialog.waitFor();
      assert.equal(await dialog.locator("img").getAttribute("src"), source);
      await page.getByRole("button", { name: "Close preview", exact: true }).click();

      // A failed full-size load must explain its cause and retry the same asset.
      for (const [status, text] of [[404, "File not found"], [403, "Image access denied"], [400, "This file cannot be previewed as an image"], [503, "Image could not be loaded. Try again."]]) {
        let failing = true;
        await page.route("**/api/chats/*/file?**", route => failing && new URL(route.request().url()).searchParams.get("image") === "1"
          ? route.fulfill({ status, json: { error: "synthetic image failure" } }) : route.continue());
        // Drop the decoded image retained by this document before simulating a
        // network failure; opening an already-loaded image needs no request.
        await page.reload();
        await card.click();
        await dialog.getByRole("status").filter({ hasText: text }).waitFor();
        if (status === 404 && surface === "mobile") await dialog.screenshot({ path: join(output, "missing-image-mobile.png") });
        failing = false;
        await dialog.getByRole("button", { name: "Retry", exact: true }).click();
        await dialog.locator("img").evaluate(async image => { if (!image.complete) await new Promise(resolve => image.addEventListener("load", resolve, { once: true })); if (!image.naturalWidth) throw new Error("Retry did not load image"); });
        assert.equal(await dialog.getByRole("status").count(), 0);
        await page.getByRole("button", { name: "Close preview", exact: true }).click();
        await page.unroute("**/api/chats/*/file?**");
      }
      await page.getByRole("button", { name: "Open saved image", exact: true }).click();
      const panel = page.locator(".file-preview");
      await panel.locator(".project-file-image").waitFor();
      const panelSource = await panel.locator("img").getAttribute("src");
      const original = await context.request.get(new URL(source, origin).href);
      const panelBytes = await context.request.get(new URL(panelSource, origin).href);
      assert.equal(panelBytes.status(), 200);
      assert.deepEqual(await panelBytes.body(), await original.body());
      const download = await panel.getByRole("link", { name: "Download file", exact: true }).getAttribute("href");
      assert.deepEqual(await (await context.request.get(new URL(download, origin).href)).body(), await original.body());
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      assert.deepEqual(errors, []);
      if (surface === "mobile") await panel.screenshot({ path: join(output, "archived-image-panel-mobile.png") });
      reports.push({ surface, archivedThumbnail: true, sharedFullPreview: true, distinctFailuresAndRetry: true, archivedFilePanel: true, originalDownload: true });
    } finally { await context.close(); context = null; }
  }
  await writeFile(join(output, "browser-report.json"), JSON.stringify(reports, null, 2) + "\n");
  console.log(JSON.stringify(reports));
} finally {
  if (context) await context.close();
  await rm(scratch, { recursive: true, force: true });
}
