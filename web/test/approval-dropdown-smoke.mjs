import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Exercise the real approval/select components with pointer and keyboard input.
// Run from the repository root through android/tools/run.sh; SPOOL_NODE_MODULES
// may point to the bundled Playwright runtime. WEBKIT_BIN can select an existing
// WebKit pw_run.sh instead of downloading another browser build.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
requireRunTemp();
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium, webkit } = createRequire(path.join(
  process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
  "__approval_dropdown__.cjs",
))("playwright");
const output = path.join(root, "build/artifacts/approval-dropdown");
const script = `
import React, {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {ApprovalCard} from '/src/approval-card.tsx';
import {translator} from '/src/i18n.ts';
import '/src/style.css';
function Scene() {
  const [scene, setScene] = useState(null);
  window.renderApproval = next => flushSync(() => setScene(next));
  return scene && <main style={{position: 'fixed', right: 16,
    width: 'min(840px, calc(100% - 32px))',
    ...(scene.bottom ? {bottom: 24} : {top: 32})}}>
    <p>我重新查看 Magpie 的最新请求统计。</p>
    <ApprovalCard key={scene.request.id} request={scene.request}
      chatID="fixture" turnID="turn" t={translator('zh-CN')}
      onError={error => { throw error; }}/>
  </main>;
}
createRoot(document.getElementById('root')).render(<Scene/>);
`;
const plugin = {
  name: "approval-dropdown-smoke",
  resolveId: id => id === "/__approval-dropdown.tsx" ? id : undefined,
  load: id => id === "/__approval-dropdown.tsx" ? script : undefined,
  configureServer(instance) {
    instance.middlewares.use(async (req, res, next) => {
      if (req.url !== "/__approval-dropdown") return next();
      res.setHeader("Content-Type", "text/html");
      res.end(await instance.transformIndexHtml(req.url,
        '<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="/__approval-dropdown.tsx"></script>'));
    });
  },
};
const deny = { id: "deny", label: "Deny", kind: "deny" };
const allow = { id: "allow", label: "Allow once", kind: "approve" };
const cancel = { id: "cancel", label: "Cancel request", kind: "cancel" };
const session = { id: "session", label: "Allow for this session", kind: "approve" };
const request = decisions => ({
  id: `fixture-${++sequence}`,
  title: "Computer Use",
  reason: 'Allow Computer Use to use "magpie"?',
  detail: "App: magpie",
  decisions,
});
let sequence = 0, server, browser;
const reports = {};
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ cacheDir: path.join(requireRunTemp(), "vite-cache"), configFile: false, root: path.join(root, "web"),
    plugins: [plugin, react()], server: { host: "127.0.0.1", port: 0, strictPort: true } });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    browser = await type.launch({ ...launch, headless: true });
    try {
      for (const [surface, width] of [["desktop", 1100], ["mobile", 390]]) {
        for (const theme of ["light", "dark"]) {
          const name = `${engine}-${surface}-${theme}`;
          const page = await browser.newPage({ viewport: { width, height: 800 } });
          const errors = [], submissions = [];
          page.setDefaultTimeout(4000);
          page.on("pageerror", error => errors.push(error.message));
          await page.route("**/api/chats/fixture/approvals?*", async route => {
            submissions.push(route.request().postDataJSON());
            await route.fulfill({ json: { ok: true } });
          });
          try {
            await page.goto(origin + "/__approval-dropdown");
            await page.waitForFunction(() => typeof window.renderApproval === "function");
            const render = async (decisions, bottom = false) => {
              await page.evaluate(scene => {
                document.documentElement.dataset.theme = scene.theme;
                window.renderApproval(scene);
              }, { request: request(decisions), bottom, theme });
            };
            const trigger = page.getByRole("button", { name: "选择审批选项", exact: true });
            const visibleMenu = async () => {
              const menu = page.getByRole("listbox");
              await menu.waitFor({ state: "visible" });
              const box = await menu.boundingBox();
              assert.ok(box && box.width >= Math.min(300, width - 16)
                && box.x >= 0 && box.y >= 0 && box.x + box.width <= width
                && box.y + box.height <= 800, `${name}: menu outside viewport ${JSON.stringify(box)}`);
              assert.ok(await menu.evaluate(element => {
                const box = element.getBoundingClientRect();
                return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + 12));
              }), `${name}: menu is covered`);
              return menu;
            };
            // This is the ordinary Computer Use request: cancel is an
            // additional choice even when no persistence scope is advertised.
            await render([deny, cancel, allow]);
            await trigger.click();
            let menu = await visibleMenu();
            assert.equal(await menu.getByRole("option").count(), 2);
            assert.equal(submissions.length, 0);
            await page.screenshot({ path: path.join(output, name + ".png") });
            await menu.getByRole("option", { name: "取消请求", exact: true }).click();
            assert.equal(await page.locator(".approval-split > .primary").textContent(), "取消请求");
            assert.equal(submissions.length, 0);
            await page.locator(".approval-split > .primary").click();
            await page.waitForFunction(() => document.querySelector('.approval-split > .primary').disabled);
            assert.equal(submissions.at(-1)?.decision, "cancel");

            await render([deny, allow, session], true);
            await trigger.click();
            menu = await visibleMenu();
            assert.equal(await menu.getAttribute("data-side"), "top");
            await menu.getByRole("option", { name: "本会话内允许", exact: true }).click();
            assert.equal(submissions.length, 1);
            assert.equal(await page.locator(".approval-split > .primary").textContent(), "本会话内允许");
            await trigger.focus();
            await page.keyboard.press("ArrowDown");
            await visibleMenu();
            await page.keyboard.press("Home");
            await page.keyboard.press("Enter");
            assert.equal(await page.locator(".approval-split > .primary").textContent(), "仅允许一次");
            assert.equal(submissions.length, 1);
            await page.locator(".approval-split > .primary").click();
            await page.waitForFunction(() => document.querySelector('.approval-split > .primary').disabled);
            assert.equal(submissions.at(-1)?.decision, "allow");

            await render([deny, allow]);
            assert.equal(await trigger.count(), 0);
            await page.locator(".approval-actions > .quiet").click();
            await page.waitForFunction(() => document.querySelector('.approval-actions > .quiet').disabled);
            assert.equal(submissions.at(-1)?.decision, "deny");
            assert.deepEqual(errors, []);
            reports[name] = { menuVisibleAndClickable: true, noImplicitSubmission: true,
              viewportConstrained: true, flipsAbove: true, keyboardSelection: true,
              loneOptionHasNoDropdown: true, denyStaysSeparate: true };
          } catch (error) {
            reports[name] = { error: String(error), errors };
            await page.screenshot({ path: path.join(output, name + ".png") });
            throw error;
          } finally {
            await page.close();
          }
        }
      }
    } finally {
      await browser.close();
      browser = undefined;
    }
  }
  console.log(JSON.stringify(reports, null, 2));
} finally {
  await fs.writeFile(path.join(output, "browser-report.json"), JSON.stringify(reports, null, 2) + "\n");
  if (browser) await browser.close();
  if (server) await server.close();
}
