// Real pointer input against the production tabs. Run through tools/run.sh.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../..", import.meta.url));
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__tabs__.cjs",
  ),
)("playwright");
const engines = [
  ["chromium", chromium, { channel: "chrome" }],
  ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
].filter(
  ([engine]) =>
    !process.env.SPOOL_SMOKE_ENGINE ||
    process.env.SPOOL_SMOKE_ENGINE === engine,
);
assert.ok(engines.length, "SPOOL_SMOKE_ENGINE must be chromium or webkit");
const cleanup = [];
const fixturePath = "/__server-tabs-fixture.js";
const fixture = {
  name: "server-tabs-pointer-fixture",
  resolveId(id) {
    if (id === fixturePath) return id;
  },
  load(id) {
    if (id !== fixturePath) return;
    return `
import { createElement, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { SegmentedTabs } from '/src/segmented-tabs.tsx';
import '/src/style.css';
const options = Array.from({ length: 8 }, (_, i) => ({ id: 'server-' + i, value: 'server-' + i, label: 'Server ' + i }));
window.fixtureSelections = [];
function Fixture() {
  const [value, select] = useState(options[0].value);
  window.fixtureSelect = select;
  return createElement('main', { style: { width: 298, margin: 24 } },
    createElement(SegmentedTabs, {
      label: 'Servers', navigation: true, className: 'desktop-server-tabs',
      options, value, onChange(id) { window.fixtureSelections.push(id); select(id); },
      trailing: createElement('a', { href: '#add', className: 'desktop-server-add', 'aria-label': 'Add server' }, '+'),
    }),
    createElement('output', null, value));
}
createRoot(document.getElementById('root')).render(createElement(Fixture));`;
  },
  configureServer(server) {
    server.middlewares.use(async (request, response, next) => {
      if (request.url !== "/server-tabs-fixture") return next();
      response.setHeader("Content-Type", "text/html");
      response.end(
        await server.transformIndexHtml(
          request.url,
          `<div id="root"></div><script type="module" src="${fixturePath}"></script>`,
        ),
      );
    });
  },
};
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [fixture],
    server: { host: "127.0.0.1", port: 0, strictPort: true },
  },
);

let browser;
try {
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of engines) {
    browser = await type.launch({ ...launch, headless: true });
    const page = await browser.newPage({
      viewport: { width: 800, height: 400 },
    });
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto(origin + "/server-tabs-fixture");
    const tabs = page.getByRole("navigation", { name: "Servers" });
    const scroll = tabs.locator(".segmented-tabs-viewport");
    await tabs.locator('[aria-current="page"]').waitFor();

    // A partially clipped tab must keep the same position between press and
    // release. Native focus scrolling can otherwise move a different label
    // under the pointer before the click is delivered.
    const target = await scroll.evaluate((scroller) => {
      const button = scroller.querySelectorAll("button")[3];
      scroller.scrollLeft = button.offsetLeft + button.offsetWidth - 20;
      const bounds = scroller.getBoundingClientRect();
      const label = button.getBoundingClientRect();
      return {
        x: bounds.x + 8,
        y: label.y + label.height / 2,
        left: scroller.scrollLeft,
        value: button.textContent.trim(),
      };
    });
    await page.mouse.move(target.x, target.y);
    await page.mouse.down();
    const pressed = await scroll.evaluate((scroller) => scroller.scrollLeft);
    assert.ok(
      Math.abs(pressed - target.left) < 1,
      `${engine}: pressing the visible edge of ${target.value} must not scroll the tabs (${target.left} → ${pressed})`,
    );
    await page.mouse.up();
    assert.equal(await page.locator("output").textContent(), "server-3");

    // A new pointer gesture takes ownership from an automatic reveal, even
    // before it moves far enough to become a drag.
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await page.evaluate(() => window.fixtureSelect("server-0"));
    await page.waitForFunction(
      () => document.querySelector(".segmented-tabs-viewport").scrollLeft < 1,
    );
    await page.evaluate(() => window.fixtureSelect("server-7"));
    await page.waitForFunction(
      () => {
        const scroller = document.querySelector(".segmented-tabs-viewport");
        return (
          scroller.scrollLeft > 10 &&
          scroller.scrollLeft < scroller.scrollWidth - scroller.clientWidth - 20
        );
      },
      undefined,
      { polling: "raf" },
    );
    const bounds = await scroll.boundingBox();
    await page.mouse.move(
      bounds.x + bounds.width / 2,
      bounds.y + bounds.height / 2,
    );
    await page.mouse.down();
    const hold = await scroll.evaluate(
      async (scroller, { x, y }) => {
        const start = scroller.scrollLeft;
        const id = document.elementFromPoint(x, y).closest("button").id;
        await new Promise((resolve) => setTimeout(resolve, 260));
        return { start, end: scroller.scrollLeft, id };
      },
      { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height / 2 },
    );
    assert.ok(
      Math.abs(hold.start - hold.end) < 1,
      `${engine}: holding the pointer must stop automatic scrolling (${hold.start} → ${hold.end})`,
    );
    await page.mouse.up();
    assert.equal(await page.locator("output").textContent(), hold.id);

    // Dragging must not activate the tab on release; a subsequent physical
    // click must activate the server whose label occupies that position.
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.evaluate(() => window.fixtureSelect("server-0"));
    await page.waitForFunction(
      () => document.querySelector(".segmented-tabs-viewport").scrollLeft < 1,
    );
    const before = await page.evaluate(() => window.fixtureSelections.length);
    await page.mouse.move(
      bounds.x + bounds.width - 12,
      bounds.y + bounds.height / 2,
    );
    await page.mouse.down();
    await page.mouse.move(bounds.x + 12, bounds.y + bounds.height / 2, {
      steps: 10,
    });
    await page.mouse.up();
    assert.equal(
      await page.evaluate(() => window.fixtureSelections.length),
      before,
    );
    assert.ok(await scroll.evaluate((scroller) => scroller.scrollLeft > 100));
    const click = await scroll.evaluate((scroller) => {
      const viewport = scroller.getBoundingClientRect();
      const button = [...scroller.querySelectorAll("button")].find((button) => {
        const box = button.getBoundingClientRect();
        return box.left >= viewport.left && box.right <= viewport.right;
      });
      const box = button.getBoundingClientRect();
      return {
        x: box.x + box.width / 2,
        y: box.y + box.height / 2,
        id: button.id,
      };
    });
    await page.mouse.click(click.x, click.y);
    assert.equal(await page.locator("output").textContent(), click.id);
    assert.equal(
      await page.evaluate(() => window.fixtureSelections.at(-1)),
      click.id,
    );
    assert.deepEqual(errors, []);
    console.log(
      `${engine}: clipped-tab clicks, interrupted reveals, and drag/click identity passed`,
    );
    await browser.close();
    browser = undefined;
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
