// Run through android/tools/run.sh with SPOOL_NODE_MODULES pointing at Playwright.
// WEBKIT_BIN may select an already-installed WebKit pw_run.sh.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { createServer as createHTTPServer } from "node:http";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.TMPDIR?.startsWith(path.join(root, "build/tmp/")));
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium, webkit } = createRequire(path.join(
  process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__online_menu__.cjs",
))("playwright");
const output = path.join(root, "build/artifacts/online-menu-dismiss");
const source = await fs.readFile(path.join(root, "web/src/main.tsx"), "utf8");
const start = source.indexOf('<details className="online">');
const end = source.indexOf("</details>", start);
assert.ok(start >= 0 && end > start, "Inspect the moved production online menu markup");
const header = source.slice(start, end + "</details>".length);
const script = `
import React, {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Users} from 'lucide-react';
import {OnlinePeopleMenu} from '/src/online-people-menu.tsx';
import {AvatarStack} from '/src/user-avatar.tsx';
import {formatUnreadCount} from '/src/direct-messages.ts';
import {translator} from '/src/i18n.ts';
import '/src/style.css';
const user={id:'alice'},t=translator('en'),unreadMessages=0,inbox={groups:[],unread:{}};
const people=[{id:'alice',nickname:'Alice',state:'online'},{id:'bob',nickname:'Bob',state:'online'}];
const chatPeerIdentity=id=>people.find(person=>person.id===id);
window.menuActions=[];
const openDirectInbox=()=>window.menuActions.push('inbox'),openDirectChat=id=>window.menuActions.push(id),setGroupPicker=()=>window.menuActions.push('new');
function Scene(){
 const [online,setOnline]=useState(people),[outside,setOutside]=useState(0),[mounted,setMounted]=useState(true);
 const displayOnline=online;
 window.updateOnline=()=>setOnline([...online,{id:'carol',nickname:'Carol',state:'away'}]);
 window.removeOnlineMenu=()=>setMounted(false);
 return <>
  <header className="header-actions" style={{position:'absolute',right:24,top:20}}>{mounted&&<>${header}</>}</header>
  <button id="outside" style={{position:'absolute',left:24,bottom:24}} onPointerDown={event=>event.stopPropagation()} onClick={()=>setOutside(outside+1)}>Outside action {outside}</button>
 </>;
}
createRoot(document.getElementById('root')).render(<Scene/>);
`;
const plugin = {
  name: "online-menu-smoke",
  resolveId: id => id === "/__online-menu.tsx" ? id : undefined,
  load: id => id === "/__online-menu.tsx" ? script : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url !== "/__online-menu") return next();
      res.setHeader("Content-Type", "text/html");
      res.end(await server.transformIndexHtml(req.url,
        '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__online-menu.tsx"></script>'));
    });
  },
};
let server, httpServer, browser;
const reports = {};
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ configFile: false, root: path.join(root, "web"), plugins: [plugin, react()],
    server: { middlewareMode: true, hmr: false } });
  httpServer = createHTTPServer(server.middlewares);
  await new Promise((resolve, reject) => { httpServer.once("error", reject); httpServer.listen(0, "127.0.0.1", resolve); });
  const origin = `http://127.0.0.1:${httpServer.address().port}`;
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    browser = await type.launch({ ...launch, headless: true });
    try {
      for (const [surface, width, touch] of [["web", 1100, false], ["desktop", 1100, false], ["mobile-web", 390, true]]) {
        for (const theme of ["light", "dark"]) {
          const name = `${engine}-${surface}-${theme}`;
          const page = await browser.newPage({ viewport: { width, height: 800 }, hasTouch: touch });
          page.setDefaultTimeout(4000);
          const errors = [];
          page.on("pageerror", error => errors.push(error.message));
          await page.addInitScript(({ surface, theme }) => {
            document.addEventListener("DOMContentLoaded", () => { document.documentElement.dataset.theme = theme; });
            if (surface === "desktop") { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; }
          }, { surface, theme });
          try {
            await page.goto(origin + "/__online-menu");
            const summary = page.locator(".online summary"), menu = page.locator(".online-list");
            const click = locator => touch ? locator.tap() : locator.click();
            const closed = () => page.waitForFunction(() => !document.querySelector(".online").open);
            await click(summary); await menu.waitFor({ state: "visible" });
            await click(menu.locator("small").first());
            assert.ok(await page.locator(".online").evaluate(element => element.open), `${name}: internal click closed the menu`);
            await click(page.locator("#outside")); await closed();
            assert.equal(await page.locator("#outside").textContent(), "Outside action 1");
            assert.deepEqual(await page.evaluate(() => window.menuActions), []);

            await click(summary); await menu.waitFor({ state: "visible" });
            await page.evaluate(() => window.updateOnline());
            await menu.getByText("Carol", { exact: true }).waitFor();
            await click(page.locator("#outside")); await closed();
            await click(summary); await menu.waitFor({ state: "visible" });
            await click(menu.getByRole("button", { name: "Bob", exact: true })); await closed();
            assert.deepEqual(await page.evaluate(() => window.menuActions), ["bob"]);

            await click(summary); await menu.waitFor({ state: "visible" });
            await click(summary); await closed();
            await click(summary); await menu.waitFor({ state: "visible" });
            await menu.getByRole("button", { name: "Bob", exact: true }).focus();
            await page.keyboard.press("Escape"); await closed();
            assert.ok(await summary.evaluate(element => element === document.activeElement));

            await click(summary); await menu.waitFor({ state: "visible" });
            await page.evaluate(() => window.removeOnlineMenu());
            await page.locator(".online").waitFor({ state: "detached" });
            await click(page.locator("#outside"));
            assert.equal(await page.locator("#outside").textContent(), "Outside action 3");
            assert.deepEqual(errors, []);
            reports[name] = { outsideDismisses: true, internalClickStaysOpen: true, outsideActionStillRuns: true,
              presenceUpdatesPreserveDismissal: true, personClickRunsOnce: true, triggerToggles: true,
              escapeRestoresFocus: true, unmountCleansUp: true };
          } catch (error) {
            reports[name] = { error: String(error), errors };
            await page.screenshot({ path: path.join(output, name + ".png") });
            throw error;
          } finally { await page.close(); }
        }
      }
    } finally { await browser.close(); browser = undefined; }
  }
  console.log(JSON.stringify(reports, null, 2));
} finally {
  await fs.writeFile(path.join(output, "browser-report.json"), JSON.stringify(reports, null, 2) + "\n");
  if (browser) await browser.close();
  if (httpServer) await new Promise(resolve => httpServer.close(resolve));
  if (server) await server.close();
}
