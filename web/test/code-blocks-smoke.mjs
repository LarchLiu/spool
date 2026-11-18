import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Real shared Markdown renderer: clipboard behavior, streamed source, and narrow layouts.
// Run with android/tools/run.sh and SPOOL_NODE_MODULES pointing to Playwright.
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
    "__code_blocks__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/code-blocks");
await fs.mkdir(output, { recursive: true });
const script = `
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {MarkdownMessage} from '/src/markdown-message.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
function Scene(){const [source,setSource]=useState('Initial'),[locale,setLocale]=useState('en');window.bind=(text,language='en')=>flushSync(()=>{setSource(text);setLocale(language)});return <main className="assistant-text" style={{padding:18}}><MarkdownMessage t={translator(locale)}>{source}</MarkdownMessage></main>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
const plugin = {
  name: "code-blocks-smoke",
  resolveId: (id) => (id === "/__code-blocks.tsx" ? id : undefined),
  load: (id) => (id === "/__code-blocks.tsx" ? script : undefined),
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url !== "/__code-blocks") return next();
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(
        await server.transformIndexHtml(
          req.url,
          '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="/__code-blocks.tsx"></script>',
        ),
      );
    });
  },
};
let server, browser;
try {
  server = await createServer({
    cacheDir: path.join(requireRunTemp(), "vite-cache"),
    configFile: false,
    root: path.join(root, "web"),
    plugins: [plugin, react()],
    server: { host: "127.0.0.1", port: 0 },
  });
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({ viewport: { width: 360, height: 800 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}/__code-blocks`,
  );
  await page.waitForFunction(() => typeof window.bind === "function");
  const report = await page.evaluate(async () => {
    const wait = () => new Promise((resolve) => setTimeout(resolve, 0));
    const highlighted = async (previous) => {
      for (let i = 0; i < 500; i++) {
        const token = document.querySelector(".markdown-code-token");
        if (token && token !== previous) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error("Code highlighting did not load");
    };
    const selectCode = (start, end) => {
      const code = document.querySelector("pre code");
      const position = (offset) => {
        const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
        let node;
        while ((node = walker.nextNode())) {
          if (offset <= node.length) return [node, offset];
          offset -= node.length;
        }
        throw new Error("Selection offset outside code");
      };
      document
        .getSelection()
        .setBaseAndExtent(...position(start), ...position(end));
    };
    const button = () => document.querySelector(".markdown-code-copy");
    const label = () => button().getAttribute("aria-label");
    const result = {};
    const copies = [];
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: async (text) => copies.push(text) },
    });
    const literal = "\tconst value = '<tag>';  \n\nreturn value;\n\n";
    window.bind("```javascript file.js\n" + literal + "\n```");
    const pre = document.querySelector("pre");
    const control = button();
    // The first grammar loads asynchronously while a selection is already active.
    selectCode(8, 2);
    await highlighted();
    result.initialHighlightRetainsSelection =
      document.getSelection().toString() === literal.slice(2, 8);
    result.highlightPreservesCodeBytes =
      pre.querySelector("code").textContent === literal + "\n";
    result.language = document.querySelector(
      ".markdown-code-language",
    ).textContent;
    control.click();
    for (let i = 0; i < 100 && label() !== "Code copied"; i++)
      await new Promise((resolve) => setTimeout(resolve, 20));
    result.whitespacePreserved = copies.at(-1) === literal;
    result.copiedFeedback = label() === "Code copied";
    const selection = document.getSelection();
    selectCode(2, 8);
    const previousToken = document.querySelector(".markdown-code-token");
    window.bind("```typescript metadata\n" + literal + "\n```");
    await highlighted(previousToken);
    result.infoChangeRetainsCodeSelection =
      pre === document.querySelector("pre") &&
      selection.toString() === literal.slice(2, 8);
    result.controlsRetained = control === button();
    // A partial fence changes its body without replacing the scroll/copy controls.
    window.bind("```typescript\n\tlatest  ");
    result.streamClearsFeedback = label() === "Copy code";
    button().click();
    await wait();
    result.copiesLatestStream = copies.at(-1) === "\tlatest  ";
    await new Promise((resolve) => setTimeout(resolve, 2100));
    result.feedbackExpires = label() === "Copy code";
    // Pending clipboard completions must not mark newly streamed code as copied.
    let resolveCopy;
    navigator.clipboard.writeText = () =>
      new Promise((resolve) => {
        resolveCopy = resolve;
      });
    button().click();
    window.bind("```ts\nnew source");
    resolveCopy();
    await wait();
    result.staleCompletionIgnored = label() === "Copy code";
    navigator.clipboard.writeText = async () => {
      throw new Error("unavailable");
    };
    const exec = document.execCommand.bind(document);
    let fallback;
    document.execCommand = (command) => {
      if (command === "copy") {
        fallback = document.activeElement.value;
        return true;
      }
      return exec(command);
    };
    button().focus();
    selectCode(1, 4);
    button().click();
    await wait();
    result.fallbackPreservesFocusAndSelection =
      fallback === "new source" &&
      document.activeElement === button() &&
      selection.toString() === "ew ";
    document.execCommand = () => false;
    button().click();
    await wait();
    result.copyFailureFeedback = label() === "Could not copy code";
    navigator.clipboard.writeText = async (text) => copies.push(text);
    button().click();
    await wait();
    result.failedCopyCanRetry =
      label() === "Code copied" && copies.at(-1) === "new source";
    window.bind("```\n```", "zh-CN");
    result.emptyCodeDisabled = button().disabled;
    result.localizedHeader =
      document.querySelector(".markdown-code-language").textContent ===
        "纯文本" && label() === "复制代码";
    window.bind("    indented code", "ja");
    button().click();
    await wait();
    result.indentedCodeAndJapanese =
      copies.at(-1) === "indented code" &&
      document.querySelector(".markdown-code-language").textContent ===
        "プレーンテキスト" &&
      label() === "コードをコピーしました";
    for (const [language, source] of [
      ["js", 'const value = "hello"; // comment'],
      ["PYTHON", 'def greet():\n\treturn "hello" # comment'],
      ["c++", "int main() { return 42; } // comment"],
      ["json", '{"message": "hello", "enabled": true}'],
    ]) {
      window.bind("```" + language + "\n" + source + "\n```");
      await highlighted();
      const tokens = [...document.querySelectorAll(".markdown-code-token")];
      result["syntaxColors_" + language] =
        new Set(tokens.map((token) => getComputedStyle(token).color)).size > 1;
    }
    const tokens = [...document.querySelectorAll(".markdown-code-token")];
    document.documentElement.dataset.theme = "light";
    const light = tokens.map((token) => getComputedStyle(token).color);
    document.documentElement.dataset.theme = "dark";
    result.themeChangesColors = tokens.some(
      (token, index) => getComputedStyle(token).color !== light[index],
    );
    for (const language of ["text", "unknown-language", ""]) {
      window.bind('```js\nconst value = "plain";\n```');
      await highlighted();
      window.bind("```" + language + '\nconst value = "plain";\n```');
      await wait();
      result["plainFallback_" + (language || "unspecified")] =
        !document.querySelector(".markdown-code-token") &&
        document.querySelector("pre code").textContent ===
          'const value = "plain";\n';
    }
    // A grammar that finishes loading after a language change cannot decorate plain text.
    window.bind("```ruby\nputs 'old'\n```");
    await wait();
    window.bind("```text\nnew source\n```");
    await new Promise((resolve) => setTimeout(resolve, 250));
    result.staleSyntaxIgnored =
      !document.querySelector(".markdown-code-token") &&
      document.querySelector("pre code").textContent === "new source\n";
    window.bind("```html\n<script>window.codeExecuted = true</script>\n```");
    await highlighted();
    result.highlightedHtmlIsInert =
      !window.codeExecuted &&
      !document.querySelector("pre script") &&
      document.querySelector("pre code").textContent ===
        "<script>window.codeExecuted = true</script>\n";
    const crossBlock = 'Before\n\n```javascript\nconst value = "hello";\n```';
    window.bind(crossBlock);
    await highlighted();
    selectCode(5, 5);
    selection.setBaseAndExtent(
      document.querySelector(".markdown p").firstChild,
      2,
      selection.focusNode,
      selection.focusOffset,
    );
    const selectedText = selection.toString();
    const selectedToken = document.querySelector(".markdown-code-token");
    window.bind(crossBlock.replace("javascript", "typescript"));
    await highlighted(selectedToken);
    result.crossBlockSelectionRetained =
      selection.toString() === selectedText.replace("javascript", "typescript");
    document.execCommand = exec;
    return result;
  });
  assert.equal(report.language, "javascript");
  for (const [name, passed] of Object.entries(report))
    if (name !== "language")
      assert.equal(passed, true, `${name}: ${JSON.stringify(report)}`);
  const layouts = [];
  for (const width of [320, 1000])
    for (const theme of ["light", "dark"]) {
      await page.setViewportSize({ width, height: 700 });
      await page.evaluate(
        ({ theme }) => {
          document.documentElement.dataset.theme = theme;
          document.activeElement?.blur();
          window.bind(
            "```\nfix: add code block headers and copy actions\n\n- Preserve whitespace\n- Keep header visible when code scrolls\n```\n\n```typescript\n" +
              "const long_identifier = 1; ".repeat(12) +
              "\n\treturn long_identifier;\n```",
          );
        },
        { theme },
      );
      await page.waitForSelector(".markdown-code-token");
      const layout = await page.evaluate(() => {
        const block = document.querySelectorAll(".markdown-code-block")[1];
        const pre = block.querySelector("pre"),
          header = block.querySelector(".markdown-code-header"),
          button = block.querySelector("button");
        const x = button.getBoundingClientRect().x;
        pre.scrollLeft = 300;
        return {
          noPageOverflow: document.documentElement.scrollWidth <= innerWidth,
          longCodeScrolls: pre.scrollWidth > pre.clientWidth,
          headerFixed:
            button.getBoundingClientRect().x === x &&
            header.scrollWidth <= header.clientWidth,
          copyVisible:
            button.getBoundingClientRect().right <= innerWidth &&
            button.getBoundingClientRect().left >= 0,
        };
      });
      for (const [name, passed] of Object.entries(layout))
        assert.equal(passed, true, `${width}/${theme}: ${name}`);
      layouts.push({ width, theme, ...layout });
      await page.evaluate(() => {
        for (const pre of document.querySelectorAll("pre")) pre.scrollLeft = 0;
      });
      await page.screenshot({
        path: path.join(output, `web-${width}-${theme}.png`),
        fullPage: true,
      });
    }
  assert.deepEqual(errors, []);
  await fs.writeFile(
    path.join(output, "browser-report.json"),
    JSON.stringify(
      { browser: browser.version(), ...report, layouts, errors },
      null,
      2,
    ) + "\n",
  );
  console.log(
    "Code block syntax, themes, selection, copy, stream updates, clipboard fallback/failure, locales, and desktop/mobile layouts passed.",
  );
} finally {
  await browser?.close();
  await server?.close();
}
