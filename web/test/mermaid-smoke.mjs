// Actual Web/desktop components and the generated Android document, without a server/provider account.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const require = createRequire(path.join(root, "web/package.json"));
const { build, createServer } = await import(require.resolve("vite"));
const { default: react } = await import(
  require.resolve("@vitejs/plugin-react")
);
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__mermaid__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/mermaid");
await fs.mkdir(output, { recursive: true });
const script = `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {MarkdownMessage} from '/src/markdown-message.tsx';import '/src/style.css';
function Scene(){const [source,setSource]=useState('Initial');window.bind=text=>flushSync(()=>setSource(text));return <main className="assistant-text" style={{padding:18}}><MarkdownMessage>{source}</MarkdownMessage></main>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
const html =
  '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__mermaid.tsx"></script>';
// Use the production policy verbatim, with only its request-specific connect
// sources replaced. A dev-only fixture misses inherited CSP restrictions.
const serverSource = await fs.readFile(
  path.join(root, "internal/workspace/server.go"),
  "utf8",
);
const policy = serverSource.match(
  /Set\("Content-Security-Policy", "([^"]+)"\+a\.realtimeConnectSources\(r\)\+"([^"]+)"\)/,
);
assert.ok(policy, "Production CSP must be present in the browser fixture");
const productionCSP = policy[1] + "'self'" + policy[2];
const productionEntry = path.join(root, "web/__mermaid.html");
let productionAssets;
const plugin = {
  name: "mermaid-smoke",
  resolveId: (id) =>
    ["/__mermaid.tsx", productionEntry].includes(id) ? id : undefined,
  load: (id) =>
    id === "/__mermaid.tsx"
      ? script
      : id === productionEntry
        ? html
        : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url.startsWith("/__production/")) {
        const name = req.url.slice("/__production/".length).split("?")[0];
        const asset = productionAssets.get(name);
        res.setHeader("Content-Security-Policy", productionCSP);
        res.setHeader("X-Frame-Options", "DENY");
        res.setHeader("X-Content-Type-Options", "nosniff");
        if (!asset) {
          res.statusCode = 404;
          res.end();
          return;
        }
        res.setHeader(
          "Content-Type",
          name.endsWith(".html")
            ? "text/html"
            : name.endsWith(".js")
              ? "text/javascript"
              : name.endsWith(".css")
                ? "text/css"
                : "application/octet-stream",
        );
        res.end(asset);
      } else if (req.url === "/__android-mermaid") {
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(
          await fs.readFile(
            path.join(root, "build/android/mermaid/assets/mermaid/index.html"),
          ),
        );
      } else if (req.url === "/__mermaid") {
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(await server.transformIndexHtml(req.url, html));
      } else next();
    });
  },
};
const diagrams = {
  flowchart: "flowchart LR\n A[开始] --> B{Ready?}\n B -->|Yes| C[Finish]",
  sequence: "sequenceDiagram\n Alice->>Bob: Hello\n Bob-->>Alice: Hi",
  class: "classDiagram\n Animal <|-- Duck\n Animal : +int age",
  state: "stateDiagram-v2\n [*] --> Idle\n Idle --> Running",
  pie: 'pie title Pets\n "Cats" : 4\n "Dogs" : 3',
  gantt:
    "gantt\n title Schedule\n dateFormat YYYY-MM-DD\n section Work\n Build :2026-11-01, 3d",
};
const fence = (source) => "```mermaid\n" + source + "\n```";
let server, browser;
const reports = [];
try {
  const built = await build({
    configFile: false,
    root: path.join(root, "web"),
    base: "./",
    plugins: [plugin, react()],
    build: { write: false, rollupOptions: { input: productionEntry } },
  });
  productionAssets = new Map(
    built.output.map((asset) => [
      asset.fileName,
      asset.type === "chunk" ? asset.code : asset.source,
    ]),
  );
  server = await createServer({
    cacheDir: path.join(requireRunTemp(), "vite-cache"),
    configFile: false,
    root: path.join(root, "web"),
    plugins: [plugin, react()],
    server: { host: "127.0.0.1", port: 0, strictPort: true, hmr: false },
  });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, options] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, {}],
  ]) {
    browser = await type.launch({ headless: true, ...options });
    const page = await browser.newPage({
      viewport: { width: 360, height: 800 },
    });
    const errors = [],
      violations = [],
      remote = [];
    await page.addInitScript(() => {
      window.cspViolations = [];
      document.addEventListener("securitypolicyviolation", (event) =>
        window.cspViolations.push({
          directive: event.violatedDirective,
          blocked: event.blockedURI,
        }),
      );
    });
    page.on("pageerror", (error) => errors.push(String(error)));
    page.on("request", (request) => {
      if (
        !request.url().startsWith(origin) &&
        !request.url().startsWith("data:")
      )
        remote.push(request.url());
    });
    const response = await page.goto(origin + "/__production/__mermaid.html");
    assert.equal(response.headers()["content-security-policy"], productionCSP);
    await page.waitForFunction(() => typeof window.bind === "function");
    const bind = (source) =>
      page.evaluate((value) => window.bind(value), source);
    const ready = () =>
      page.locator('.markdown-mermaid[data-ready="true"]').waitFor();
    const frame = () => page.frameLocator(".markdown-mermaid-frame");
    const texts = {};
    for (const [name, source] of Object.entries(diagrams)) {
      await bind(fence(source));
      await ready();
      texts[name] = await frame().locator("#diagram svg").textContent();
      assert.ok(texts[name].length > 0, `${engine}/${name}`);
    }
    await bind(fence(diagrams.flowchart));
    await ready();
    await page.evaluate(() => {
      window.copied = [];
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: { writeText: async (text) => window.copied.push(text) },
      });
    });
    await page.getByRole("button", { name: "Copy code", exact: true }).click();
    await page
      .getByRole("button", { name: "Code copied", exact: true })
      .waitFor();
    assert.equal(
      await page.evaluate(() => window.copied.at(-1)),
      diagrams.flowchart,
    );
    const originalFrame = await page
      .locator(".markdown-mermaid-frame")
      .elementHandle();
    await page.getByRole("button", { name: "Source", exact: true }).click();
    await page.locator("pre .markdown-code-token").first().waitFor();
    assert.ok(
      (await page
        .locator("pre .markdown-code-token")
        .evaluateAll(
          (nodes) =>
            new Set(nodes.map((node) => getComputedStyle(node).color)).size,
        )) > 1,
      `${engine}: Mermaid source uses syntax colors`,
    );
    assert.equal(
      await page.locator("pre code").textContent(),
      diagrams.flowchart + "\n",
    );
    await page.screenshot({
      path: path.join(output, `${engine}-source.png`),
      fullPage: true,
    });
    await page.getByRole("button", { name: "Preview", exact: true }).click();
    await ready();
    assert.equal(
      await originalFrame.evaluate((node) => node.isConnected),
      true,
      `${engine}: closing a diagram restores focus to its trigger`,
    );
    const controls = await page
      .locator(".markdown-code-header button")
      .evaluateAll((nodes) =>
        nodes.map((node) => node.getAttribute("aria-label")),
      );
    assert.deepEqual(controls.slice(0, 2), ["Source", "Enlarge diagram"]);
    assert.match(controls[2], /^(Copy code|Code copied)$/);
    // Observe the entire transition, including frames before Playwright can query the dialog.
    const watchLoading = (source) =>
      page.evaluate((source) => {
        window.previewLoading = { source: false, loading: false };
        window.previewObserver = new MutationObserver(() => {
          const dialog = document.querySelector("dialog.mermaid-preview");
          if (!dialog) return;
          if (dialog.querySelector("pre")) window.previewLoading.source = true;
          if (
            dialog.querySelector('[role="status"]')?.textContent ===
            "Rendering diagram…"
          )
            window.previewLoading.loading = true;
        });
        window.previewObserver.observe(document.body, {
          childList: true,
          subtree: true,
        });
        if (source !== undefined) window.bind(source);
      }, source);
    const checkLoading = async () => {
      const result = await page.evaluate(() => {
        window.previewObserver.disconnect();
        return window.previewLoading;
      });
      assert.deepEqual(
        result,
        { source: false, loading: true },
        "Loading must not flash source",
      );
    };
    await watchLoading();
    await page
      .getByRole("button", { name: "Enlarge diagram", exact: true })
      .click();
    const dialog = page.getByRole("dialog", { name: "Enlarge diagram" });
    await dialog.locator('.markdown-mermaid[data-ready="true"]').waitFor();
    await checkLoading();
    const fullFrame = await (
      await dialog.locator("iframe").elementHandle()
    ).contentFrame();
    assert.equal(await dialog.getByRole("status").count(), 0);
    const size = () =>
      fullFrame
        .locator("#diagram svg")
        .evaluate((node) => node.getBoundingClientRect().width);
    const initial = await size();
    await dialog.getByRole("button", { name: "Zoom in", exact: true }).click();
    await fullFrame.waitForFunction(
      (width) =>
        document.querySelector("#diagram svg").getBoundingClientRect().width >
        width * 1.2,
      initial,
    );
    await dialog.getByRole("button", { name: "Zoom out", exact: true }).click();
    await fullFrame.waitForFunction(
      (width) =>
        Math.abs(
          document.querySelector("#diagram svg").getBoundingClientRect().width -
            width,
        ) < 1,
      initial,
    );
    for (let i = 0; i < 5; i++)
      await dialog
        .getByRole("button", { name: "Zoom in", exact: true })
        .click();
    const bounds = await dialog.locator("iframe").boundingBox();
    const beforePan = await fullFrame
      .locator("#diagram")
      .evaluate((node) => node.scrollLeft);
    await page.mouse.move(bounds.x + 160, bounds.y + 120);
    await page.mouse.down();
    await page.mouse.move(bounds.x + 80, bounds.y + 120, { steps: 5 });
    await page.mouse.up();
    assert.ok(
      (await fullFrame
        .locator("#diagram")
        .evaluate((node) => node.scrollLeft)) > beforePan,
      "Enlarged diagrams can be dragged",
    );
    await dialog
      .getByRole("button", { name: "Fit diagram", exact: true })
      .click();
    await fullFrame.waitForFunction(
      (width) =>
        Math.abs(
          document.querySelector("#diagram svg").getBoundingClientRect().width -
            width,
        ) < 1,
      initial,
    );
    await page.screenshot({
      path: path.join(output, `${engine}-enlarged.png`),
      fullPage: true,
    });
    await watchLoading(fence("flowchart LR\n Updated --> Preview"));
    await fullFrame
      .locator("#diagram svg")
      .filter({ hasText: "Updated" })
      .waitFor();
    await checkLoading();
    await bind(fence("not a diagram"));
    await dialog.locator("pre code").waitFor();
    assert.equal(
      await dialog.locator("pre code").textContent(),
      "not a diagram",
    );
    assert.equal(await dialog.getByRole("status").count(), 0);
    await watchLoading(fence("flowchart LR\n Latest --> Preview"));
    await fullFrame
      .locator("#diagram svg")
      .filter({ hasText: "Latest" })
      .waitFor();
    await checkLoading();
    await page.keyboard.press("Escape");
    await dialog.waitFor({ state: "detached" });
    assert.equal(
      await page
        .getByRole("button", { name: "Enlarge diagram", exact: true })
        .evaluate((node) => node === document.activeElement),
      true,
    );
    // Rapid streamed edits must not allow old async completions to replace the latest diagram.
    await bind("```mermaid\nflowchart LR\n A[Incomplete");
    await page
      .locator('.markdown-mermaid[data-ready="false"] pre code')
      .waitFor();
    await page.waitForTimeout(500);
    await bind(fence("flowchart LR\n Newest --> Result"));
    await ready();
    assert.match(await frame().locator("#diagram svg").textContent(), /Newest/);
    for (let i = 0; i < 8; i++)
      await bind(fence(`flowchart LR\n Node${i} --> End`));
    await ready();
    assert.match(await frame().locator("#diagram svg").textContent(), /Node7/);
    // The renderer remains usable after a completely invalid diagram.
    await bind(fence("not a diagram"));
    await page.waitForTimeout(500);
    assert.equal(
      await page
        .locator('.markdown-mermaid[data-ready="false"] pre code')
        .textContent(),
      "not a diagram\n",
    );
    await bind(fence(diagrams.sequence));
    await ready();
    const layouts = [];
    for (const width of [320, 1000])
      for (const theme of ["light", "dark"]) {
        await bind("Switching layout");
        await page.setViewportSize({ width, height: 800 });
        await page.evaluate(
          ({ source, theme }) => {
            document.documentElement.dataset.theme = theme;
            window.bind(source);
          },
          {
            source:
              fence(diagrams.flowchart) + "\n\n" + fence(diagrams.sequence),
            theme,
          },
        );
        await page.waitForFunction(
          () =>
            document.querySelectorAll('.markdown-mermaid[data-ready="true"]')
              .length === 2,
        );
        assert.equal(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
          true,
          `${engine}/${width}/${theme} page overflow`,
        );
        const svg = page
          .frameLocator(".markdown-mermaid-frame")
          .first()
          .locator("#diagram svg");
        const fill = await svg
          .locator(".node rect")
          .first()
          .evaluate((node) => getComputedStyle(node).fill);
        layouts.push({ width, theme, fill });
        await page.screenshot({
          path: path.join(output, `${engine}-${width}-${theme}.png`),
          fullPage: true,
        });
      }
    assert.notEqual(
      layouts[0].fill,
      layouts[1].fill,
      "Theme change should recolor the diagram",
    );
    for (const current of page.frames())
      violations.push(...(await current.evaluate(() => window.cspViolations)));
    assert.deepEqual(
      violations,
      [],
      "Production diagrams and viewer must render without CSP violations",
    );
    // Dev has no enclosing CSP that could hide an SVG hyperlink regression.
    await page.goto(origin + "/__mermaid");
    await page.waitForFunction(() => typeof window.bind === "function");
    const linked =
      'flowchart LR\n A[Linked node] --> B[Safe]\n click A "https://example.com/" _self\n click B "https://example.com/other" _blank';
    await bind(fence(linked));
    await ready();
    const rendererFrame = await (
      await page.locator("iframe").elementHandle()
    ).contentFrame();
    const rendererURL = rendererFrame.url();
    const anchors = frame().locator("#diagram svg a");
    assert.equal(
      await anchors.count(),
      2,
      "Link directives still render their nodes",
    );
    for (const anchor of await anchors.all()) {
      assert.equal(await anchor.getAttribute("href"), null);
      assert.equal(await anchor.getAttribute("xlink:href"), null);
      await anchor.click();
    }
    assert.equal(rendererFrame.url(), rendererURL);
    assert.equal(page.url(), origin + "/__mermaid");
    await bind(fence("flowchart LR\n AfterClick --> Updated"));
    await ready();
    assert.match(
      await frame().locator("#diagram svg").textContent(),
      /AfterClick/,
    );
    // Content cannot escape the sandbox, execute label scripts, navigate the app or fetch images/styles.
    const hostile =
      '%%{init: {"securityLevel":"loose","htmlLabels":true,"themeCSS":"@import url(https://example.com/track.css);"}}%%\nflowchart LR\n A["<img src=https://example.com/track.png onerror=parent.hacked=true>"] --> B[Safe]\n click B "javascript:parent.hacked=true"';
    await bind(fence(hostile));
    await page.waitForTimeout(1000);
    assert.equal(await page.evaluate(() => !!window.hacked), false);
    assert.deepEqual(remote, [], "No remote resources may be requested");
    await bind("```javascript\nconst ordinary = 1;\n```");
    assert.equal(await page.locator("iframe").count(), 0);
    assert.match(await page.locator("pre code").textContent(), /ordinary/);
    // Execute the exact generated Android HTML and its bridge protocol in a real browser.
    const native = await browser.newPage({
      viewport: { width: 360, height: 800 },
    });
    native.on("pageerror", (error) => errors.push(String(error)));
    await native.addInitScript(() => {
      window.nativeResults = [];
      window.SpoolMermaid = {
        rendered: (generation, ready, height) =>
          window.nativeResults.push({ generation, ready, height }),
      };
    });
    await native.goto(origin + "/__android-mermaid");
    await native.waitForFunction(
      () => typeof window.spoolRenderMermaid === "function",
    );
    for (const [index, [name, source]] of Object.entries(diagrams).entries()) {
      const generation = index + 1;
      await native.evaluate(
        ({ source, generation }) =>
          window.spoolRenderMermaid(source, false, generation),
        { source, generation },
      );
      await native.waitForFunction(
        (generation) =>
          window.nativeResults.some(
            (value) =>
              value.generation === generation &&
              value.ready &&
              value.height > 0,
          ),
        generation,
      );
      assert.equal(
        await native.locator("#diagram svg").textContent(),
        texts[name],
        `${engine}/${name}: Android matches shared renderer`,
      );
    }
    await native.evaluate(() =>
      window.spoolRenderMermaid("invalid", true, 100),
    );
    await native.waitForFunction(() =>
      window.nativeResults.some(
        (value) => value.generation === 100 && !value.ready,
      ),
    );
    await native.evaluate(
      (source) => window.spoolRenderMermaid(source, true, 102, true),
      diagrams.flowchart,
    );
    await native.waitForFunction(() =>
      window.nativeResults.some(
        (value) => value.generation === 102 && value.ready,
      ),
    );
    const nativeWidth = await native
      .locator("#diagram svg")
      .evaluate((node) => node.getBoundingClientRect().width);
    await native.evaluate(() => window.spoolZoomMermaid("in"));
    assert.ok(
      (await native
        .locator("#diagram svg")
        .evaluate((node) => node.getBoundingClientRect().width)) >
        nativeWidth * 1.2,
    );
    await native.evaluate(
      (source) => window.spoolRenderMermaid(source, true, 101),
      diagrams.flowchart,
    );
    await native.waitForFunction(() =>
      window.nativeResults.some(
        (value) => value.generation === 101 && value.ready,
      ),
    );
    await native.evaluate(
      (source) => window.spoolRenderMermaid(source, false, 103),
      linked,
    );
    await native.waitForFunction(() =>
      window.nativeResults.some(
        (value) => value.generation === 103 && value.ready,
      ),
    );
    for (const anchor of await native.locator("#diagram svg a").all()) {
      assert.equal(await anchor.getAttribute("href"), null);
      assert.equal(await anchor.getAttribute("xlink:href"), null);
      await anchor.click();
    }
    assert.equal(native.url(), origin + "/__android-mermaid");
    assert.deepEqual(errors, []);
    reports.push({
      engine,
      version: browser.version(),
      diagrams: Object.keys(diagrams),
      layouts,
      nativeDocument: true,
      sourceToggle: true,
      enlargedZoomAndPan: true,
      productionCSP,
      violations,
      inertLinks: true,
      remote,
      errors,
    });
    await browser.close();
    browser = undefined;
  }
  await fs.writeFile(
    path.join(output, "browser-report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
  console.log(
    "Mermaid: Chromium/WebKit diagrams, copy, streams, recovery, themes, layout, isolation and generated Android document passed.",
  );
} finally {
  await browser?.close();
  await server?.close();
}
