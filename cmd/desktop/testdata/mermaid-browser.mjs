import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { requireRunTemp } from "../../../tools/test-scratch.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../../../", import.meta.url));
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__mermaid__.cjs",
  ),
)("playwright");
const [url] = process.argv.slice(2);
for (const [engine, type, options] of [
  ["chromium", chromium, { channel: "chrome" }],
  ["webkit", webkit, {}],
]) {
  const browser = await type.launch({ headless: true, ...options });
  try {
    const page = await browser.newPage();
    const failures = [],
      remote = [];
    page.on("pageerror", (error) => failures.push(String(error)));
    page.on("response", (response) => {
      if (!response.ok())
        failures.push(`${response.status()} ${response.url()}`);
    });
    page.on("request", (request) => {
      if (new URL(request.url()).origin !== new URL(url).origin)
        remote.push(request.url());
    });
    await page.goto(url);
    const iframe = page.frameLocator("iframe");
    const render = (source, generation) =>
      page.evaluate(
        ({ source, generation }) =>
          document
            .querySelector("iframe")
            .contentWindow.postMessage(
              {
                type: "spool:render-mermaid",
                source,
                generation,
                dark: false,
                expanded: true,
              },
              "*",
            ),
        { source, generation },
      );
    await render(
      'flowchart LR\n A[Desktop] --> B[Offline]\n click A "https://example.com/"',
      1,
    );
    await iframe.locator("#diagram svg").waitFor();
    assert.match(await iframe.locator("#diagram svg").textContent(), /Desktop/);
    const anchor = iframe.locator("#diagram svg a");
    assert.equal(await anchor.getAttribute("href"), null);
    assert.equal(await anchor.getAttribute("xlink:href"), null);
    await anchor.click();
    await render("flowchart LR\n Updated --> Result", 2);
    await iframe
      .locator("#diagram svg")
      .filter({ hasText: "Updated" })
      .waitFor();
    const width = await iframe
      .locator("#diagram svg")
      .evaluate((node) => node.getBoundingClientRect().width);
    await page.evaluate(() =>
      document
        .querySelector("iframe")
        .contentWindow.postMessage(
          { type: "spool:zoom-mermaid", action: "in" },
          "*",
        ),
    );
    await assert.doesNotReject(async () => {
      const frame = await (
        await page.locator("iframe").elementHandle()
      ).contentFrame();
      await frame.waitForFunction(
        (width) =>
          document.querySelector("#diagram svg").getBoundingClientRect().width >
          width * 1.2,
        width,
      );
    });
    assert.equal(page.url(), url);
    assert.deepEqual(failures, []);
    assert.deepEqual(remote, []);
    console.log(
      `${engine}: desktop gateway rendered, updated and zoomed an inert diagram`,
    );
  } finally {
    await browser.close();
  }
}
