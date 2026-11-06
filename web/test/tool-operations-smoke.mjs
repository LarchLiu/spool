// Run through android/tools/run.sh with SPOOL_NODE_MODULES pointing at Playwright.
// Exercise the actual disclosure DOM through raw/lazy and live input changes.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(path.resolve(process.env.TMPDIR || "").startsWith(path.join(root, "build/tmp") + path.sep));
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium } = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__tool_operations__.cjs"))("playwright");
const fixtures = JSON.parse(await fs.readFile(path.join(root, "android/app/src/test/resources/tool-operations.json"), "utf8"));
const output = path.join(root, "build/artifacts/tool-classification");
const script = `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {ToolActivityList} from '/src/tool-activity.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
function Scene(){const [parts,setParts]=useState([]);window.bind=parts=>flushSync(()=>setParts(parts));
return <div style={{padding:24}}><ToolActivityList parts={parts} live={true} backend="codex" t={translator('en')} summary="Working"/></div>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
let server, browser, apiRequests = 0;
const plugin = {
  name: "tool-operations-smoke",
  resolveId: id => id === "/__tool-operations.tsx" ? id : undefined,
  load: id => id === "/__tool-operations.tsx" ? script : undefined,
  configureServer(instance) {
    instance.middlewares.use(async (req, res, next) => {
      if (req.url?.startsWith("/api/")) { apiRequests++; res.statusCode = 500; res.end("Unexpected tool details request"); return; }
      if (req.url === "/__tool-operations") {
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(await instance.transformIndexHtml(req.url, '<!doctype html><div id="root"></div><script type="module" src="/__tool-operations.tsx"></script>'));
        return;
      }
      next();
    });
  },
};
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ configFile: false, root: path.join(root, "web"), plugins: [plugin, react()], server: { host: "127.0.0.1", port: 0 } });
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 700 } });
  const errors = [];
  page.on("pageerror", error => errors.push(String(error)));
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__tool-operations`);
  await page.waitForFunction(() => typeof window.bind === "function");
  let count = 0;
  for (const fixture of fixtures.filter(fixture => fixture.operations.length)) for (const lazy of [false, true]) {
    const part = { id: "call", role: "assistant", kind: "tool_use", name: fixture.name, text: fixture.input ? JSON.stringify(fixture.input) : fixture.text };
    if (lazy) { part.text = ""; part.tool = { kind: "shell", operations: fixture.operations, href: "/chats/chat/tool-detail?ref=call" }; }
    await page.evaluate(part => window.bind([part, { id: "call", role: "tool", kind: "tool_result", text: "combined output" }]), part);
    const group = page.locator(".tool-activity-compound");
    assert.equal(await group.count(), 1, fixture.id);
    assert.equal(await group.locator("button.tool-activity-summary").getAttribute("aria-expanded"), "false", fixture.id);
    assert.equal(await page.locator(".tool-activity-static").count(), 0, fixture.id);
    await group.locator("button.tool-activity-summary").click();
    await page.waitForFunction(count => document.querySelectorAll(".tool-activity-static").length === count, fixture.operations.length);
    const rows = page.locator(".tool-activity-static");
    assert.equal(await rows.count(), fixture.operations.length, fixture.id);
    assert.equal(await page.locator("button.tool-activity-item-summary, .tool-detail").count(), 0, fixture.id);
    for (const row of await rows.all()) {
      assert.equal(await row.getAttribute("aria-expanded"), null, fixture.id);
      assert.equal(await row.locator(".tool-activity-caret").count(), 0, fixture.id);
      await row.click();
    }
    assert.equal(await page.locator(".tool-detail").count(), 0, fixture.id);
    await group.locator("button.tool-activity-summary").click();
    assert.equal(await page.locator(".tool-activity-static").count(), 0, fixture.id);
    count++;
  }
  // Same keyed row starts expanded, then becomes a pipeline. Its old state
  // must not mount details or issue a lazy body request for the derived row.
  const single = { id: "live", role: "assistant", kind: "tool_use", name: "Bash", text: '{"command":"cat README.md"}' };
  await page.evaluate(part => window.bind([part]), single);
  await page.locator("button.tool-activity-item-summary").click();
  assert.equal(await page.locator(".tool-detail").count(), 1);
  assert.match(await page.locator(".tool-detail").textContent(), /cat README.md/);
  await page.evaluate(part => window.bind([{ ...part, text: "", tool: { kind: "read", operations: [{ kind: "read", command: "cat README.md | head -20", path: "README.md" }], href: "/chats/chat/tool-detail?ref=live" } }]), single);
  assert.equal(await page.locator(".tool-activity-static").count(), 0);
  assert.equal(await page.locator(".tool-detail").count(), 0);
  await page.locator(".tool-activity-compound button.tool-activity-summary").click();
  assert.equal(await page.locator(".tool-activity-static").count(), 1);
  await page.locator(".tool-activity-static").click();
  assert.equal(await page.locator(".tool-detail").count(), 0);
  await page.evaluate(part => window.bind([part]), single);
  assert.equal(await page.locator("button.tool-activity-item-summary").count(), 1);
  assert.equal(await page.locator(".tool-detail").count(), 1);
  // Render the user's example at desktop/mobile widths for visual inspection.
  const example = fixtures[0];
  await page.evaluate(fixture => window.bind([{ role: "assistant", kind: "tool_use", name: fixture.name, text: JSON.stringify(fixture.input) }]), example);
  assert.equal(await page.locator(".tool-activity-compound button.tool-activity-summary").textContent(), "Ran command, read files");
  for (const [mode, width] of [["desktop", 1100], ["mobile", 390]]) {
    await page.setViewportSize({ width, height: 700 });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(await page.locator(".tool-activity-history").count(), 1);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.screenshot({ path: path.join(output, `operations-${mode}.png`) });
    await page.locator(".tool-activity-compound button.tool-activity-summary").click();
    assert.equal(await page.locator(".tool-activity-static").count(), 3);
    await page.screenshot({ path: path.join(output, `operations-${mode}-expanded.png`) });
    await page.locator(".tool-activity-compound button.tool-activity-summary").click();
  }
  const call = id => ({ id, role: "assistant", kind: "tool_use", name: example.name, text: JSON.stringify(example.input) });
  await page.evaluate(parts => window.bind(parts), [call("one"), call("two")]);
  assert.equal(await page.locator(".tool-activity-compound").count(), 2);
  await page.locator(".tool-activity-compound button.tool-activity-summary").first().click();
  await page.evaluate(parts => window.bind(parts), [call("one"), call("two"), { role: "assistant", kind: "text", text: "Checking results" }, call("three")]);
  assert.equal(await page.locator(".tool-activity-compound").count(), 3);
  assert.equal(await page.locator(".tool-activity-static").count(), 3);
  assert.equal(await page.locator(".tool-activity-compound button.tool-activity-summary").first().getAttribute("aria-expanded"), "true");
  await page.locator(".work-label-toggle").click();
  assert.equal(await page.locator(".tool-activity-compound").count(), 0);
  await page.locator(".work-label-toggle").click();
  assert.equal(await page.locator(".tool-activity-static").count(), 3);
  assert.equal(apiRequests, 0);
  assert.deepEqual(errors, []);
  const report = { fixtureProjections: count, compoundCallsInitiallyFolded: true, uniqueKindSummaries: true, independentCallGroups: true, expansionSurvivesLiveUpdates: true, staticRowsHaveNoButtonCaretOrDetails: true, staleExpansionSuppressed: true, ordinaryToolDetailsPreserved: true, detailRequests: apiRequests, javascriptErrors: errors };
  await fs.writeFile(path.join(output, "operations-browser-report.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(report);
} finally {
  if (browser) await browser.close();
  if (server) await server.close();
}
