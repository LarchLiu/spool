import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Exercise the production Markdown actions and FileSidebar with synthetic files.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
requireRunTemp();
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(
  require.resolve("@vitejs/plugin-react")
);
const { chromium } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__file_citations__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/file-citations");
const filePath = "/work/output/附件二_入场验收清单_表格版样例.pdf";
const source = `结果：:codex-file-citation{path=${JSON.stringify(filePath)} purpose="output"}\n\n:codex-file-citation{path="/work/notes.md" purpose="output"}`;
const script = `
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {MarkdownMessage,MarkdownPreview} from '/src/markdown-message.tsx';import {FileSidebar} from '/src/file-sidebar.tsx';import {LocalFileContext} from '/src/local-file-context.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
function Scene(){const [source,setSource]=useState(${JSON.stringify(source)}),[selection,setSelection]=useState(null),[active,setActive]=useState(false);window.bind=value=>flushSync(()=>setSource(value));const open=target=>{window.opened=target;setSelection(old=>({target,scope:'test',request:(old?.request||0)+1}));setActive(true)};
return <div className={'app left-collapsed'+(active?'':' right-collapsed')} style={{'--right-panel-width':'460px'}}><main className="workspace assistant-text" style={{padding:24}}><LocalFileContext.Provider value={{openFile:open}}><MarkdownMessage>{source}</MarkdownMessage></LocalFileContext.Provider><button className="summary"><MarkdownPreview>{source}</MarkdownPreview></button></main><FileSidebar project={{id:'project',name:'Citation test'}} chatID="chat" workspace={{path:'/work',kind:'main',available:true}} onRefreshWorkspace={()=>{}} active={active} mobileOpen={active} width={460} selection={selection} t={translator('zh-CN')} theme="light" onOpen={open} onClose={()=>setActive(false)} onResize={()=>{}} onClear={()=>setSelection(null)}/></div>}
createRoot(document.getElementById('root')).render(<Scene/>);`;

// Minimal real PDF with an xref table, kept in memory rather than a scratch file.
function pdfFixture() {
  let data = "%PDF-1.4\n";
  const offsets = [0];
  const body = "BT /F1 22 Tf 40 150 Td (File citation preview works) Tj ET";
  for (const object of [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 240] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
    `<< /Length ${body.length} >>\nstream\n${body}\nendstream`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
  ]) {
    offsets.push(Buffer.byteLength(data));
    data += `${offsets.length - 1} 0 obj\n${object}\nendobj\n`;
  }
  const xref = Buffer.byteLength(data);
  data += `xref\n0 ${offsets.length}\n0000000000 65535 f \n`;
  data += offsets
    .slice(1)
    .map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`)
    .join("");
  data += `trailer\n<< /Size ${offsets.length} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(data);
}
const pdf = pdfFixture();
const requests = [];
const plugin = {
  name: "file-citations-smoke",
  resolveId: (id) => (id === "/__file-citations.tsx" ? id : undefined),
  load: (id) => (id === "/__file-citations.tsx" ? script : undefined),
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url === "/__file-citations") {
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(
          await server.transformIndexHtml(
            req.url,
            '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="/__file-citations.tsx"></script>',
          ),
        );
        return;
      }
      if (!req.url.startsWith("/api/")) return next();
      const url = new URL(req.url, "http://localhost");
      requests.push(url.pathname + url.search);
      if (url.pathname.endsWith("/files")) {
        res.setHeader("Content-Type", "application/json");
        res.end(JSON.stringify({ path: ".", entries: [], truncated: false }));
      } else if (url.searchParams.has("preview")) {
        res.setHeader("Content-Type", "application/pdf");
        res.setHeader("Content-Disposition", 'inline; filename="report.pdf"');
        res.setHeader("X-Content-Type-Options", "nosniff");
        res.setHeader("X-Frame-Options", "SAMEORIGIN");
        res.setHeader(
          "Content-Security-Policy",
          "default-src 'none'; object-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'",
        );
        res.end(pdf);
      } else {
        const target = url.searchParams.get("path");
        res.setHeader("Content-Type", "application/json");
        res.end(
          JSON.stringify({
            path: target,
            name: target.split("/").pop(),
            kind: target.endsWith(".pdf") ? "binary" : "text",
            media_type: target.endsWith(".pdf")
              ? "application/pdf"
              : "text/plain",
            size: pdf.length,
            content: target.endsWith(".md") ? "# File citation notes" : null,
            external: true,
          }),
        );
      }
    });
  },
};
let server, browser;
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ cacheDir: path.join(requireRunTemp(), "vite-cache"),
    configFile: false,
    root: path.join(root, "web"),
    plugins: [plugin, react()],
    server: { host: "127.0.0.1", port: 0 },
  });
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({
    viewport: { width: 1200, height: 900 },
  });
  const errors = [];
  const consoleMessages = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.on("console", (message) =>
    consoleMessages.push(message.type() + ": " + message.text()),
  );
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}/__file-citations`,
  );
  await page.waitForFunction(() => typeof window.bind === "function");
  assert.deepEqual(
    requests,
    [],
    "Citations must not load files before a click",
  );
  assert.equal(await page.locator(".summary button, .summary a").count(), 0);
  assert.equal(
    await page.locator(".local-file-link").first().textContent(),
    filePath.split("/").pop(),
  );
  const nativePDF = page.waitForEvent("framenavigated", {
    predicate: (frame) =>
      frame.parentFrame()?.url().startsWith("chrome-extension://") &&
      frame.url().includes("preview=1"),
  });
  await page.locator(".local-file-link").first().click();
  await page.waitForFunction(
    () => !!document.querySelector(".file-pdf-preview"),
  );
  assert.deepEqual(await page.evaluate(() => window.opened), {
    path: filePath,
  });
  try {
    const frame = await nativePDF;
    await frame.locator("embed").waitFor({ state: "attached", timeout: 5000 });
  } catch (error) {
    await page.screenshot({
      path: path.join(output, "desktop.png"),
      fullPage: true,
    });
    console.log({
      consoleMessages,
      requests,
      frames: page.frames().map((frame) => frame.url()),
    });
    throw error;
  }
  assert.ok(
    requests.some(
      (request) =>
        new URL(request, "http://localhost").searchParams.get("preview") ===
        "1",
    ),
  );
  assert.ok(
    requests.every((request) => request.startsWith("/api/chats/chat/")),
    "Preview stays scoped to its chat",
  );
  await page.screenshot({
    path: path.join(output, "desktop.png"),
    fullPage: true,
  });
  await page.locator(".local-file-link").nth(1).click();
  await page.getByRole("button", { name: "预览", exact: true }).click();
  await page.locator(".file-preview h1").waitFor();
  assert.equal(
    await page.locator(".file-preview h1").textContent(),
    "File citation notes",
  );
  const partial = ':codex-file-citation{path="/work/stream.pdf" purpose="out';
  await page.evaluate((value) => window.bind(value), partial);
  assert.equal(await page.locator(".local-file-link").count(), 0);
  await page.evaluate((value) => window.bind(value), partial + 'put"}');
  assert.equal(await page.locator(".local-file-link").count(), 1);
  const streamedPDF = page.waitForEvent("framenavigated", {
    predicate: (frame) =>
      frame.parentFrame()?.url().startsWith("chrome-extension://") &&
      frame.url().includes("preview=1"),
  });
  await page.locator(".local-file-link").click();
  await page.locator(".file-pdf-preview").waitFor();
  await (await streamedPDF).locator("embed").waitFor({ state: "attached" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
  });
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({
    path: path.join(output, "mobile.png"),
    fullPage: true,
  });
  assert.deepEqual(errors, []);
  const report = {
    citationFilename: true,
    clickOpensFileSidebar: true,
    inlinePDF: true,
    existingMarkdownPreview: true,
    streamingCitation: true,
    chatScopedFiles: true,
    noAutomaticRequests: true,
    javascriptErrors: errors,
  };
  await fs.writeFile(
    path.join(output, "browser-report.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  console.log(report);
} finally {
  if (browser) await browser.close();
  if (server) await server.close();
}
