// Real Spool dialog and upstream clipboard/keyboard code, with synthetic desktop
// messages only. No host capture, native input injection or Spool user data.
// Run through tools/run.sh with VOXL_SOURCE and SPOOL_NODE_MODULES (Playwright).
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.VOXL_SOURCE, "Set VOXL_SOURCE to upstream voxl source");
const source = resolve(process.env.VOXL_SOURCE);
const { chromium } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "web/node_modules"),
    "__voxl__.cjs",
  ),
)("playwright");
const scratch = await mkdtemp(join(requireRunTemp(), "voxl-clipboard-"));
const output = join(root, "build/artifacts/voxl-clipboard");
const parentFixture = `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { RemoteDesktopDialog } from './web/src/remote-desktop-settings.tsx';
import { translator } from './web/src/i18n.ts';
createRoot(document.getElementById('root')).render(
  <RemoteDesktopDialog t={translator('zh-CN')} onClose={() => {}} />
);
`;
const viewerFixture = `
import { createClipboard } from ${JSON.stringify(join(source, "web/src/client/clipboard.ts"))};
import { setupKeyboard } from ${JSON.stringify(join(source, "web/src/client/keyboard.ts"))};
const commands = [], images = [], events = [];
const app = {
  kbd: document.getElementById('kbd'), surface: document.getElementById('surface'),
  inIframe: true, isMac: /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent), remoteMac: false,
  send: command => { commands.push(command); events.push(command); },
  sendBin: (type, bytes) => { images.push([type, Array.from(bytes)]); events.push('image'); },
};
const clipboard = createClipboard(app);
setupKeyboard(app, clipboard, clipboard.flush);
document.getElementById('status').style.display = 'none';
window.viewer = { app, clipboard, commands, images, events };
`;
let server, context;
try {
  const bundle = async (contents, loader) =>
    (
      await build({
        stdin: { contents, resolveDir: root, loader },
        nodePaths: [join(root, "web/node_modules")],
        bundle: true,
        write: false,
        format: "iife",
        jsx: "automatic",
      })
    ).outputFiles[0].text;
  const parentJS = await bundle(parentFixture, "tsx");
  const viewerJS = await bundle(viewerFixture, "ts");
  const viewerHTML = (
    await readFile(join(source, "web/index.html"), "utf8")
  ).replaceAll("{{title}}", "Spool");
  server = createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    if (
      url.pathname === "/api/desktop/connect" ||
      url.pathname === "/api/desktop"
    ) {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ token: "fixture", available: true }));
    } else if (url.pathname === "/api/desktop/view/") {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(viewerHTML);
    } else if (
      url.pathname === "/parent.js" ||
      url.pathname === "/api/desktop/view/client.js"
    ) {
      res.setHeader("Content-Type", "text/javascript; charset=utf-8");
      res.end(url.pathname === "/parent.js" ? parentJS : viewerJS);
    } else if (url.pathname === "/") {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(`<!doctype html><meta charset="utf-8"><style>
        dialog {width:90vw;height:85vh} iframe {width:100%;height:85%;border:0}
      </style><div id="root"></div><script src="/parent.js"></script>`);
    } else {
      res.writeHead(404).end();
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  context = await chromium.launchPersistentContext(join(scratch, "profile"), {
    channel: "chrome",
    headless: true,
    viewport: { width: 1100, height: 800 },
  });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin,
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(origin);
  await page.waitForFunction(
    () => !!document.querySelector("iframe")?.contentWindow?.viewer,
  );
  const frame = page
    .frames()
    .find((frame) => frame.url().includes("/api/desktop/view/"));
  assert.ok(frame);
  assert.equal(
    await page.locator("iframe").getAttribute("allow"),
    "fullscreen; clipboard-read; clipboard-write",
  );

  // Unfocused remote updates wait, then use the real browser Clipboard API.
  await page.getByRole("button", { name: "断开连接" }).focus();
  await page.evaluate(() => navigator.clipboard.writeText("local sentinel"));
  await frame.evaluate(() =>
    viewer.clipboard.onRemoteText("远程复制\ntext, 😀"),
  );
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "local sentinel",
  );
  await frame.evaluate(() => {
    viewer.app.kbd.focus();
    viewer.clipboard.flush();
  });
  await page.waitForFunction(
    async () => (await navigator.clipboard.readText()) === "远程复制\ntext, 😀",
  );

  // Native paste must send clipboard data before the remote paste shortcut.
  const localText = "本地粘贴\ntext, 😀";
  await frame.evaluate(
    (text) => navigator.clipboard.writeText(text),
    localText,
  );
  await frame.evaluate(() => {
    viewer.commands.length = 0;
    viewer.app.kbd.focus();
  });
  const pasteShortcut = await frame.evaluate(() =>
    viewer.app.isMac ? "Meta+v" : "Control+v",
  );
  await page.keyboard.press(pasteShortcut, { delay: 50 });
  await frame.waitForFunction(() =>
    viewer.commands.some((command) => command.startsWith("c,")),
  );
  assert.deepEqual(
    await frame.evaluate(() =>
      viewer.commands.filter(
        (c) => c.startsWith("c,") || c.startsWith("k,118,"),
      ),
    ),
    [`c,${localText}`, "k,118,1", "k,118,0"],
  );
  const touchResult = await frame.evaluate(() => {
    const { app, commands } = viewer;
    commands.length = 0;
    app.kbd.focus();
    // Dispatching a paste also covers touchscreen/context-menu paste delivery.
    const data = new DataTransfer();
    data.setData("text/plain", "触屏粘贴");
    app.kbd.dispatchEvent(
      new ClipboardEvent("paste", { clipboardData: data, cancelable: true }),
    );
    return commands.filter((c) => c.startsWith("c,"));
  });
  assert.deepEqual(touchResult, ["c,触屏粘贴"]);

  const png = await frame.evaluate(async () => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 2;
    canvas.getContext("2d").fillRect(0, 0, 2, 2);
    const blob = await new Promise((resolve) =>
      canvas.toBlob(resolve, "image/png"),
    );
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  });
  await frame.evaluate((png) => {
    viewer.app.kbd.focus();
    viewer.clipboard.onRemoteImage(new Uint8Array([0x04, ...png]).buffer);
  }, png);
  await frame.waitForFunction(async () =>
    (await navigator.clipboard.read()).some((item) =>
      item.types.includes("image/png"),
    ),
  );
  const imageBytes = await frame.evaluate(async () => {
    const item = (await navigator.clipboard.read()).find((item) =>
      item.types.includes("image/png"),
    );
    return Array.from(
      new Uint8Array(await (await item.getType("image/png")).arrayBuffer()),
    );
  });
  const pixels = (bytes) =>
    frame.evaluate(async (bytes) => {
      const image = await createImageBitmap(
        new Blob([new Uint8Array(bytes)], { type: "image/png" }),
      );
      const canvas = document.createElement("canvas");
      canvas.width = image.width;
      canvas.height = image.height;
      const ctx = canvas.getContext("2d");
      ctx.drawImage(image, 0, 0);
      image.close();
      return [
        canvas.width,
        canvas.height,
        Array.from(ctx.getImageData(0, 0, canvas.width, canvas.height).data),
      ];
    }, bytes);
  // Browsers may re-encode PNGs in the clipboard; compare decoded pixels.
  assert.deepEqual(await pixels(imageBytes), await pixels(png));
  await frame.evaluate(() => {
    viewer.commands.length = 0;
    viewer.images.length = 0;
    viewer.events.length = 0;
  });
  await page.keyboard.press(pasteShortcut, { delay: 50 });
  await frame.waitForFunction(() => viewer.images.length === 1);
  const [imageType, pastedImage] = await frame.evaluate(() => viewer.images[0]);
  assert.equal(imageType, 0x11);
  assert.deepEqual(await pixels(pastedImage), await pixels(png));
  await frame.waitForFunction(() => viewer.commands.includes("k,118,0"));
  assert.equal(
    await frame.evaluate(
      () => viewer.events.indexOf("image") < viewer.events.indexOf("k,118,1"),
    ),
    true,
  );
  assert.deepEqual(errors, []);
  await mkdir(output, { recursive: true });
  await writeFile(
    join(output, "browser-report.json"),
    JSON.stringify(
      {
        iframePermissions: true,
        deferredRemoteText: true,
        localTextPaste: true,
        touchPaste: true,
        remoteImage: true,
        localImagePaste: true,
        errors,
      },
      null,
      2,
    ) + "\n",
  );
  console.log(
    "Voxl clipboard: iframe permissions, text/image sync, focus retry and native paste passed.",
  );
} finally {
  await context?.close();
  if (server) await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
