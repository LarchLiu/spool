// Run against TestUserAttachmentBrowser's isolated fixture server. Do not retain
// native messages, document contents, browser profiles, or login data as reports.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.TMPDIR?.startsWith(path.join(root, "build/tmp/")));
const { chromium } = createRequire(
  path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__attachment__.cjs"),
)("playwright");
const origin = process.env.SPOOL_ATTACHMENT_ORIGIN;
assert.match(origin || "", /^http:\/\/127\.0\.0\.1:\d+$/);
const output = path.join(root, "build/artifacts/attachment-access");
const errors = [];
let browser;
try {
  browser = await chromium.launch({ executablePath: process.env.CHROME_BIN, headless: true });
  for (const [surface, width] of [["web", 1280], ["desktop", 1280], ["mobile", 390]]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: "block" });
    try {
      if (surface === "desktop") await context.addInitScript(() => { window.spoolDesktop = true; });
      const login = await context.request.post(origin + "/api/login", {
        data: { username: "owner", password: "test-password-2026" },
      });
      assert.equal(login.status(), 200);
      const page = await context.newPage();
      page.on("pageerror", error => errors.push(error.message));
      let downloads = 0;
      page.on("download", () => downloads++);
      await page.goto(origin + "/?chat=" + process.env.SPOOL_ATTACHMENT_CHAT);
      const card = page.locator(".user-row .attachment-open").first();
      await card.waitFor();
      assert.equal(await card.evaluate(element => element.tagName), "BUTTON", surface);
      await card.click();
      await page.locator(".file-preview .office-document").waitFor();
      assert.ok((await page.locator(".office-document").textContent()).trim().length > 0);
      assert.equal(downloads, 0, surface + " previews the card without downloading");
      const download = await page.getByRole("link", { name: "Download file", exact: true }).getAttribute("href");
      const original = await context.request.get(new URL(download, origin).href);
      assert.equal(original.status(), 200);
      assert.equal((await original.body()).length, Number(process.env.SPOOL_ATTACHMENT_BYTES));
      await page.getByRole("button", { name: "Close file preview", exact: true }).click();
      await page.locator(".file-preview").waitFor({ state: "detached" });
      if (width < 900) await page.getByRole("button", { name: "Collapse file sidebar", exact: true }).click();
      await page.locator(".assistant-text .local-file-link").filter({ hasText: "Open attachment" }).click();
      await page.locator(".file-preview .office-document").waitFor();
      assert.equal(await page.locator(".file-preview [role=alert]").count(), 0);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    } finally {
      await context.close();
    }
  }
  assert.deepEqual(errors, []);
  const report = { webAttachmentPreview: true, desktopAttachmentPreview: true, mobileWebAttachmentPreview: true, transcriptLinksPreview: true, originalDownloadPreserved: true, javascriptErrors: errors };
  await fs.mkdir(output, { recursive: true });
  await fs.writeFile(path.join(output, "browser-report.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report));
} finally {
  if (browser) await browser.close();
}
