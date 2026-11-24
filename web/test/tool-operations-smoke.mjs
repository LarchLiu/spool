import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Run through android/tools/run.sh with SPOOL_NODE_MODULES pointing at Playwright.
// Exercise the actual disclosure DOM through raw/lazy and live input changes.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { createServer as createPortProbe } from "node:net";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
requireRunTemp();
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium } = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__tool_operations__.cjs"))("playwright");
const fixtures = JSON.parse(await fs.readFile(path.join(root, "android/app/src/test/resources/tool-operations.json"), "utf8"));
const output = path.join(root, "build/artifacts/native-tool-items");
const script = `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {ToolActivityList} from '/src/tool-activity.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
function Scene(){const [parts,setParts]=useState([]),[language,setLanguage]=useState('en');window.bind=(parts,language='en')=>flushSync(()=>{setParts(parts);setLanguage(language)});
return <div style={{padding:24}}><ToolActivityList parts={parts} live={true} backend="codex" t={translator(language)} summary="Working"/></div>}
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
  const probe = createPortProbe();
  await new Promise((resolve, reject) => { probe.once("error", reject); probe.listen(0, "127.0.0.1", resolve); });
  const port = probe.address().port;
  await new Promise((resolve, reject) => probe.close(error => error ? reject(error) : resolve()));
  server = await createServer({ cacheDir: path.join(requireRunTemp(), "vite-cache"), configFile: false, root: path.join(root, "web"), plugins: [plugin, react()], server: { host: "127.0.0.1", port, strictPort: true } });
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
    assert.equal(await group.locator("button.tool-activity-item-summary").count(), 1, fixture.id);
    assert.match(await group.locator("button.tool-activity-item-summary").textContent(), /Command and output/, fixture.id);
    for (const row of await rows.all()) {
      assert.equal(await row.getAttribute("aria-expanded"), null, fixture.id);
      assert.equal(await row.locator(".tool-activity-caret").count(), 0, fixture.id);
      await row.click();
    }
    assert.equal(await page.locator(".tool-detail").count(), 0, fixture.id);
    if (!lazy) {
      await group.locator("button.tool-activity-item-summary").click();
      assert.equal(await group.locator(".tool-detail").count(), 1, fixture.id);
      assert.match(await group.locator(".tool-detail").textContent(), /combined output/, fixture.id);
      await group.locator("button.tool-activity-item-summary").click();
    }
    await group.locator("button.tool-activity-summary").click();
    assert.equal(await page.locator(".tool-activity-static").count(), 0, fixture.id);
    count++;
  }
  // Same keyed row starts expanded, then becomes a compound command. Its old state
  // must not mount details or issue a lazy body request for the derived row.
  const single = { id: "live", role: "assistant", kind: "tool_use", name: "Bash", text: '{"command":"cat README.md"}' };
  await page.evaluate(part => window.bind([part]), single);
  await page.locator("button.tool-activity-item-summary").click();
  assert.equal(await page.locator(".tool-detail").count(), 1);
  assert.match(await page.locator(".tool-detail").textContent(), /cat README.md/);
  await page.evaluate(part => window.bind([{ ...part, text: "", tool: { kind: "read", operations: [{ kind: "read", command: "cat README.md", path: "README.md" }, {kind: "list", command: "ls"}], href: "/chats/chat/tool-detail?ref=live" } }]), single);
  assert.equal(await page.locator(".tool-activity-static").count(), 0);
  assert.equal(await page.locator(".tool-detail").count(), 0);
  await page.locator(".tool-activity-compound button.tool-activity-summary").click();
  assert.equal(await page.locator(".tool-activity-static").count(), 2);
  await page.locator(".tool-activity-static").first().click();
  assert.equal(await page.locator(".tool-detail").count(), 0);
  await page.evaluate(part => window.bind([part]), single);
  assert.equal(await page.locator("button.tool-activity-item-summary").count(), 1);
  assert.equal(await page.locator(".tool-detail").count(), 1);
  // Render the user's example at desktop/mobile widths for visual inspection.
  const example = fixtures[0];
  await page.evaluate(fixture => window.bind([{ role: "assistant", kind: "tool_use", name: fixture.name, text: JSON.stringify(fixture.input) }]), example);
  assert.equal(await page.locator(".tool-activity-compound button.tool-activity-summary").textContent(), "Ran command, read files, searched files");
  for (const [mode, width] of [["desktop", 1100], ["mobile", 390]]) {
    await page.setViewportSize({ width, height: 700 });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(await page.locator(".tool-activity-history").count(), 1);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.screenshot({ path: path.join(output, `operations-${mode}.png`) });
    await page.locator(".tool-activity-compound button.tool-activity-summary").click();
    assert.equal(await page.locator(".tool-activity-static").count(), 3);
    await page.screenshot({ path: path.join(output, `operations-${mode}-expanded.png`) });
    await page.locator(".tool-activity-whole button").click();
    assert.equal(await page.locator(".tool-detail").count(), 1);
    await page.screenshot({ path: path.join(output, `operations-${mode}-details.png`) });
    await page.locator(".tool-activity-whole button").click();
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
  // One outcome belongs to the whole execution, including failed edits.
  await page.evaluate(() => window.bind([
    {id: "failed", role: "assistant", kind: "tool_use", name: "fileChange", text: "*** Update File: a.txt", tool: {kind: "write", status: "failed", exit_code: 2, path: "a.txt", changes: {additions: 3, deletions: 1}}},
    {id: "declined", role: "assistant", kind: "tool_use", name: "fileChange", text: "*** Add File: b.txt", tool: {kind: "create", status: "declined", path: "b.txt", changes: {additions: 2, deletions: 0}}},
  ]));
  assert.match(await page.locator(".tool-activity-history").textContent(), /Exit code 2/);
  assert.match(await page.locator(".tool-activity-history").textContent(), /Declined/);
  assert.equal(await page.locator(".tool-activity-stats").count(), 0);
  await page.screenshot({ path: path.join(output, "outcomes-mobile.png") });
  // Summary counts keep their diff colors and precede the disclosure arrow,
  // including matching positive/negative baselines, grouped edits and live counts.
  const edit = (id, additions, deletions) => ({ id, role: "assistant", kind: "tool_use", name: "fileChange", text: "", tool: {
    kind: "write", status: "completed", path: `${id}-long-file-name-for-a-narrow-summary.ts`, changes: { additions, deletions },
  } });
  const headerCases = [["single.txt", 1, 1], ["unequal.txt", 23, 12], ["added.txt", 46, 0], ["removed.txt", 0, 8]];
  const patch = headerCases.map(([name, additions, deletions]) => [
    `diff --git a/${name} b/${name}`, `--- a/${name}`, `+++ b/${name}`,
    `@@ -${deletions ? 1 : 0},${deletions} +${additions ? 1 : 0},${additions} @@`,
    ...Array.from({ length: deletions }, (_, i) => `-old line ${i}`),
    ...Array.from({ length: additions }, (_, i) => `+new line ${i}`), "",
  ].join("\n")).join("");
  let statsScenes = 0;
  for (const language of ["en", "zh-CN"]) for (const theme of ["light", "dark"]) for (const width of [1100, 390]) {
    await page.setViewportSize({ width, height: 700 });
    await page.emulateMedia({ colorScheme: theme });
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; window.bind([]); }, theme);
    await page.evaluate(({ parts, language }) => window.bind(parts, language), { parts: [edit("one", 43, 7), edit("two", 3, 1)], language });
    await page.waitForFunction(() => document.querySelectorAll(".tool-activity-stats").length === 3);
    for (const counts of [["+46 −8", "+43 −7", "+3 −1"], ["+2 −4", "+0 −4", "+2 −0"]]) {
      const rows = await page.locator(".tool-activity-stats").evaluateAll(nodes => nodes.map(stats => {
        const row = stats.parentElement, label = row.children[1], caret = row.querySelector(".tool-activity-caret");
        const addition = stats.firstElementChild, deletion = stats.lastElementChild;
        const baseline = element => {
          const marker = document.createElement("span");
          marker.style.cssText = "display:inline-block;width:0;height:0;vertical-align:baseline";
          element.append(marker);
          const y = marker.getBoundingClientRect().top;
          marker.remove();
          return y;
        };
        return { text: stats.textContent, addition: getComputedStyle(addition).color, deletion: getComputedStyle(deletion).color,
          baselineOffset: Math.abs(baseline(addition) - baseline(deletion)),
          ordered: label.getBoundingClientRect().right <= stats.getBoundingClientRect().left && stats.getBoundingClientRect().right <= caret.getBoundingClientRect().left,
          fits: caret.getBoundingClientRect().right <= row.getBoundingClientRect().right };
      }));
      assert.deepEqual(rows.map(row => row.text), counts);
      for (const row of rows) {
        assert.equal(row.addition, theme === "dark" ? "rgb(7, 196, 128)" : "rgb(24, 164, 108)");
        assert.equal(row.deletion, theme === "dark" ? "rgb(255, 46, 63)" : "rgb(213, 44, 54)");
        assert.ok(row.baselineOffset <= 0.5, `${language}/${theme}/${width}: positive and negative counts share a baseline (${row.baselineOffset}px)`);
        assert.ok(row.ordered && row.fits, `${theme}/${width}: statistics precede the caret within the summary`);
      }
      await page.evaluate(({ parts, language }) => window.bind(parts, language), { parts: [edit("one", 0, 4), edit("two", 2, 0)], language });
    }
    await page.screenshot({ path: path.join(output, `diff-stats-${language}-${width}-${theme}.png`) });
    await page.evaluate(({ patch, language }) => window.bind([
      { id: "patch-counts", role: "assistant", kind: "tool_use", name: "fileChange", text: "" },
      { id: "patch-counts", role: "tool", kind: "tool_result", text: patch },
    ], language), { patch, language });
    const patchRow = page.locator("button.tool-activity-item-summary");
    if (await patchRow.getAttribute("aria-expanded") === "false") await patchRow.click();
    await page.locator(".tool-diff [data-diffs-header]").last().waitFor();
    const headerStats = page.locator(".tool-diff .tool-activity-stats");
    assert.deepEqual(await headerStats.allTextContents(), ["+1 −1", "+23 −12", "+46 −0", "+0 −8"]);
    const headerOffsets = await headerStats.evaluateAll(nodes => nodes.map(stats => {
      const baseline = element => {
        const marker = document.createElement("span");
        marker.style.cssText = "display:inline-block;width:0;height:0;vertical-align:baseline";
        element.append(marker);
        const y = marker.getBoundingClientRect().top;
        marker.remove();
        return y;
      };
      return Math.abs(baseline(stats.firstElementChild) - baseline(stats.lastElementChild));
    }));
    assert.ok(headerOffsets.every(offset => offset <= 0.5), `${language}/${theme}/${width}: diff header counts share a baseline (${headerOffsets})`);
    assert.equal(await page.locator(".tool-diff [data-additions-count]:visible, .tool-diff [data-deletions-count]:visible").count(), 0, "Each diff has one visible set of counters");
    await page.locator(".tool-diff").first().screenshot({ path: path.join(output, `diff-header-${language}-${width}-${theme}.png`) });
    statsScenes++;
  }
  assert.equal(apiRequests, 0);
  assert.deepEqual(errors, []);
  const report = { fixtureProjections: count, compoundCallsInitiallyFolded: true, uniqueKindSummaries: true, independentCallGroups: true, expansionSurvivesLiveUpdates: true, staticRowsHaveNoButtonCaretOrDetails: true, staleExpansionSuppressed: true, ordinaryToolDetailsPreserved: true, diffStatsScenes: statsScenes, detailRequests: apiRequests, javascriptErrors: errors };
  await fs.writeFile(path.join(output, "operations-browser-report.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(report);
} finally {
  if (browser) await browser.close();
  if (server) await server.close();
}
