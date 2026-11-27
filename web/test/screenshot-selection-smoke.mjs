// Real pointer interaction with the screenshot selection surface in both engines.
// Run: SPOOL_NODE_MODULES=<playwright location> sh tools/run.sh screenshot-selection bun web/test/screenshot-selection-smoke.mjs
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";
const scratch = requireRunTemp();
const root = resolve(import.meta.dirname, "../..");
const output = join(root, "build/artifacts/screenshot-selection");
const { chromium, webkit } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "web/node_modules"),
    "__screenshot__.cjs",
  ),
)("playwright");
const cleanups = [];
const entry = join(root, "web/src/screenshot-main.tsx");
const server = await createTestViteServer(
  { after: (fn) => cleanups.push(fn) },
  {
    configFile: false,
    root: join(root, "web"),
    plugins: [
      react(),
      {
        name: "mock-selection-navigation",
        transform(source, id) {
          if (id === entry)
            return source.replaceAll(
              "location.assign(",
              "window.captureNavigate(",
            );
        },
      },
    ],
    server: { host: "127.0.0.1", port: 0 },
  },
);
let browser;
const reports = [];
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  for (const [engine, type, options] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, {}],
  ]) {
    browser = await type.launchPersistentContext(join(scratch, engine), {
      ...options,
      headless: true,
      viewport: { width: 1000, height: 700 },
    });
    const page = browser.pages()[0];
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    await page.addInitScript(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 2000;
      canvas.height = 1400;
      const ctx = canvas.getContext("2d");
      ctx.fillStyle = "#384b63";
      ctx.fillRect(0, 0, 2000, 1400);
      ctx.fillStyle = "#ccd9e9";
      ctx.fillRect(160, 120, 1200, 960);
      ctx.fillStyle = "#102741";
      ctx.fillRect(560, 320, 1100, 760);
      // Fine texture makes mosaic behavior observable on every part of the
      // synthetic desktop without reading any actual display pixels.
      const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height);
      for (let y = 0; y < canvas.height; y++) {
        for (let x = 0; x < canvas.width; x++) {
          if (((x >> 2) + (y >> 2)) % 2) {
            const offset = (y * canvas.width + x) * 4;
            for (let c = 0; c < 3; c++) pixels.data[offset + c] += 18;
          }
        }
      }
      ctx.putImageData(pixels, 0, 0);
      window.spoolScreenshot = {
        token: "fixture",
        image: canvas.toDataURL(),
        locale: "en",
        theme: "light",
        bounds: { x: -1000, y: 120, width: 1000, height: 700 },
        cursor: { x: 400, y: 250 },
        windows: [
          {
            id: 7,
            name: "Front app",
            bounds: { x: -720, y: 280, width: 550, height: 380 },
          },
          {
            id: 8,
            name: "Back app",
            bounds: { x: -920, y: 180, width: 600, height: 480 },
          },
        ],
      };
      if (new URL(location.href).searchParams.has("picked")) {
        window.spoolScreenshot.selected = true;
        window.spoolScreenshot.selection = {
          x: 200.5,
          y: 150.5,
          width: 300.5,
          height: 200.5,
        };
        window.spoolScreenshot.windows = [];
      }
      window.captureRequests = [];
      window.captureExports = [];
      window.captureLoads = 0;
      const content = {
        image: window.spoolScreenshot.image,
        windows: window.spoolScreenshot.windows,
      };
      window.spoolScreenshot.image = "";
      window.spoolScreenshot.windows = [];
      window.mygo = {
        async call(method, token, data) {
          if (method === "DesktopScreenshot.Load" && token === "fixture") {
            window.captureLoads++;
            const mode = new URL(location.href).searchParams.get("capture");
            if (mode === "fail") throw new Error("synthetic capture failure");
            if (mode === "pending")
              await new Promise((resolve) => {
                window.releaseCapture = resolve;
              });
            return content;
          }
          if (method !== "DesktopScreenshot.Stage" || token !== "fixture")
            throw new Error("unexpected export");
          if (window.failNextCapture) {
            window.failNextCapture = false;
            throw new Error("synthetic save failure");
          }
          await new Promise((resolve) => setTimeout(resolve, 120));
          window.captureExports.push(data);
        },
      };
      window.captureNavigate = (target) =>
        window.captureRequests.push(
          Object.fromEntries(new URL(target).searchParams),
        );
    });
    const load = async () => {
      await page.goto(
        `http://127.0.0.1:${server.httpServer.address().port}/screenshot.html`,
      );
      await page.waitForFunction(() =>
        window.captureRequests.some((request) => request.action === "ready"),
      );
    };
    const rect = () =>
      page.locator(".capture-selection").evaluate((element) => ({
        x: parseInt(element.style.left),
        y: parseInt(element.style.top),
        width: parseInt(element.style.width),
        height: parseInt(element.style.height),
      }));
    const drag = async (a, b) => {
      await page.mouse.move(a.x, a.y);
      await page.mouse.down();
      await page.mouse.move(b.x, b.y, { steps: 5 });
      await page.mouse.up();
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve)),
          ),
      );
    };
    const exportSize = () =>
      page.evaluate(async () => {
        const image = new Image();
        image.src = "data:image/png;base64," + window.captureExports.at(-1);
        await image.decode();
        return { width: image.naturalWidth, height: image.naturalHeight };
      });
    const previewPNG = () =>
      page.locator(".capture-preview").evaluate(async (canvas) => {
        await new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        );
        const bytes = canvas
          .getContext("2d")
          .getImageData(0, 0, canvas.width, canvas.height).data;
        return Array.from(
          new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
        ).join(",");
      });
    const pixel = (x, y) =>
      page
        .locator(".capture-preview")
        .evaluate(
          (canvas, { x, y }) =>
            Array.from(canvas.getContext("2d").getImageData(x, y, 1, 1).data),
          { x, y },
        );
    const waitExport = () =>
      page.waitForFunction(
        () => window.captureRequests.at(-1).action === "complete",
      );
    const checkTooltip = async (button) => {
      const title = await button.getAttribute("title");
      assert.ok(title, "every toolbar icon has a title");
      await button.hover();
      const tooltip = page.getByRole("tooltip");
      await tooltip.waitFor({ state: "visible" });
      assert.equal(await tooltip.textContent(), title);
      const box = await tooltip.boundingBox();
      assert.ok(
        box.x >= 0 &&
          box.y >= 0 &&
          box.x + box.width <= 1000 &&
          box.y + box.height <= 700,
        `${title}: hover text stays inside the display`,
      );
    };
    await load();
    assert.equal(await page.evaluate(() => window.captureLoads), 1);
    assert.equal(
      await page.locator(".capture-toolbar").count(),
      0,
      "opening the picker must not show an instruction toolbar",
    );
    assert.deepEqual(await rect(), { x: 280, y: 160, width: 550, height: 380 });
    assert.equal(
      await page.locator(".capture-size").textContent(),
      "Front app · 550 × 380",
      "window hover shows its name followed by width and height",
    );
    await page.screenshot({ path: join(output, `${engine}-window-hover.png`) });
    await page.mouse.move(100, 100);
    assert.deepEqual(await rect(), { x: 80, y: 60, width: 600, height: 480 });
    assert.equal(
      await page.locator(".capture-size").textContent(),
      "Back app · 600 × 480",
    );
    await page.mouse.click(100, 100);
    assert.deepEqual(await rect(), { x: 80, y: 60, width: 600, height: 480 });
    assert.equal(
      await page.locator(".capture-size").textContent(),
      "600 × 480",
    );
    assert.equal(await page.locator(".editable").count(), 1);
    // macOS WebKit follows the system keyboard-access preference; Option+Tab
    // includes toolbar buttons even when plain Tab skips non-text controls.
    const nextControl = engine === "webkit" ? "Alt+Tab" : "Tab";
    await page.keyboard.press(nextControl);
    assert.deepEqual(
      await page.evaluate(() => ({
        tool: document.activeElement?.getAttribute("data-tool"),
        visible: document.activeElement?.matches(":focus-visible"),
      })),
      { tool: "move", visible: true },
      `${engine}: Tab still exposes keyboard focus`,
    );
    assert.equal(
      await page.getByRole("tooltip").textContent(),
      "Move selection",
      "keyboard focus also exposes the tool label",
    );
    await page.mouse.click(100, 100);
    assert.equal(
      await page.locator('.capture-toolbar [aria-pressed="true"]').count(),
      0,
      "window selection must start without a selected tool",
    );
    assert.equal(
      await page.locator(".capture-toolbar button:focus-visible").count(),
      0,
      "pointer selection returns focus to the surface",
    );
    assert.deepEqual(
      await page
        .locator(".capture-toolbar button")
        .evaluateAll((buttons) => [
          ...new Set(
            buttons
              .filter((button) => !button.matches(":hover"))
              .map((button) => getComputedStyle(button).backgroundColor),
          ),
        ]),
      ["rgba(0, 0, 0, 0)"],
      "ordinary toolbar icons have no background, including confirm",
    );
    for (const button of await page.locator(".capture-toolbar button").all()) {
      await checkTooltip(button);
    }
    await page.getByRole("button", { name: "Mosaic", exact: true }).hover();
    await page.screenshot({ path: join(output, `${engine}-tooltips.png`) });
    await drag({ x: 130, y: 130 }, { x: 142, y: 138 });
    assert.deepEqual(
      await rect(),
      { x: 92, y: 68, width: 600, height: 480 },
      "without an active tool, dragging inside moves the crop",
    );
    assert.equal(
      await page
        .getByRole("button", { name: "Undo", exact: true })
        .isDisabled(),
      true,
      "neutral dragging must not draw an annotation",
    );
    assert.equal(
      await page.evaluate(() => window.captureExports.length),
      0,
      "window selection must wait for editing/confirmation",
    );
    await page.getByRole("button", { name: "Capture", exact: true }).click();
    await waitExport();
    assert.deepEqual(await exportSize(), { width: 1200, height: 960 });
    await load();
    await page.mouse.click(400, 250);
    assert.deepEqual(await rect(), { x: 280, y: 160, width: 550, height: 380 });
    const rectangle = page.getByRole("button", {
      name: "Rectangle",
      exact: true,
    });
    await rectangle.click();
    assert.equal(await rectangle.getAttribute("aria-pressed"), "true");
    await rectangle.click();
    assert.equal(
      await rectangle.getAttribute("aria-pressed"),
      "false",
      "clicking an active tool deselects it",
    );
    assert.equal(
      await page.locator(".capture-toolbar button:focus-visible").count(),
      0,
      "pointer tool clicks must not leave focus rings",
    );
    await page.keyboard.press(nextControl);
    const focusedTool = await page.evaluate(() =>
      document.activeElement?.getAttribute("data-tool"),
    );
    assert.ok(
      ["move", "rect", "line", "mosaic"].includes(focusedTool),
      `${engine}: keyboard navigation reaches an editing tool`,
    );
    await page.keyboard.press("Enter");
    assert.equal(
      await page
        .locator(`button[data-tool="${focusedTool}"]`)
        .getAttribute("aria-pressed"),
      "true",
      "Enter activates the focused tool instead of saving",
    );
    assert.equal(await page.evaluate(() => window.captureExports.length), 0);
    await page.keyboard.press("Space");
    assert.equal(
      await page.locator('.capture-toolbar [aria-pressed="true"]').count(),
      0,
    );
    await page.mouse.click(400, 250);
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    await waitExport();
    assert.deepEqual(await exportSize(), { width: 1100, height: 760 });
    assert.equal(
      await page.evaluate(() => window.captureExports.length),
      1,
      "busy exports must not duplicate",
    );
    await load();
    await drag({ x: 200, y: 150 }, { x: 600, y: 450 });
    assert.deepEqual(await rect(), { x: 200, y: 150, width: 400, height: 300 });
    assert.equal(await page.locator(".capture-handle").count(), 8);
    assert.equal(
      await page.evaluate(
        () =>
          window.captureRequests.filter((r) => r.action === "complete").length,
      ),
      0,
      "Dragging must wait for explicit confirmation",
    );
    await page.screenshot({ path: join(output, `${engine}-selection.png`) });
    await page.evaluate(() => {
      document.documentElement.dataset.theme = "dark";
    });
    await page.screenshot({
      path: join(output, `${engine}-selection-dark.png`),
    });
    await page.evaluate(() => {
      document.documentElement.dataset.theme = "light";
    });
    for (const handle of ["nw", "n", "ne", "e", "se", "s", "sw", "w"]) {
      const initial = await rect();
      const target = await page
        .locator(`.capture-handle.handle-${handle}`)
        .boundingBox();
      await drag(
        { x: target.x + target.width / 2, y: target.y + target.height / 2 },
        {
          x: target.x + target.width / 2 + 12,
          y: target.y + target.height / 2 + 10,
        },
      );
      const changed = await rect();
      assert.notDeepEqual(changed, initial, `${handle} must resize`);
      assert.ok(changed.width > 0 && changed.height > 0);
    }
    for (const edge of ["n", "e", "s", "w"]) {
      const before = await rect();
      // Pick a point away from midpoint and corners to exercise the entire edge.
      const x =
        edge === "w"
          ? before.x
          : edge === "e"
            ? before.x + before.width
            : before.x + before.width / 4;
      const y =
        edge === "n"
          ? before.y
          : edge === "s"
            ? before.y + before.height
            : before.y + before.height / 4;
      await drag({ x, y }, { x: x + 10, y: y + 10 });
      assert.notDeepEqual(
        await rect(),
        before,
        `${edge} edge must resize away from handles`,
      );
    }
    let before = await rect();
    await page
      .getByRole("button", { name: "Move selection", exact: true })
      .click();
    await drag(
      { x: before.x + 40, y: before.y + 40 },
      { x: before.x + 65, y: before.y + 65 },
    );
    assert.deepEqual(await rect(), {
      ...before,
      x: before.x + 25,
      y: before.y + 25,
    });
    const final = await rect();
    const a = { x: final.x + 40, y: final.y + 40 };
    const b = { x: final.x + 120, y: final.y + 100 };
    await page.getByRole("button", { name: "Rectangle", exact: true }).click();
    const clean = await previewPNG();
    await drag(a, b);
    await page.waitForFunction(
      () => !document.querySelector('button[aria-label="Undo"]').disabled,
    );
    assert.deepEqual(
      await pixel(80, 130),
      [183, 78, 45, 255],
      "rectangle uses the theme color at source resolution",
    );
    await page.keyboard.press("Control+z");
    assert.equal(
      await previewPNG(),
      clean,
      "keyboard undo restores the source pixels",
    );
    assert.equal(
      await page
        .getByRole("button", { name: "Undo", exact: true })
        .isDisabled(),
      true,
    );
    await drag(a, b);
    await page.getByRole("button", { name: "Line", exact: true }).click();
    await drag({ x: a.x, y: b.y + 20 }, { x: b.x, y: b.y + 20 });
    assert.deepEqual(
      await pixel(140, 240),
      [183, 78, 45, 255],
      "line is composited into preview",
    );
    const drawn = await previewPNG();
    await page.getByRole("button", { name: "Mosaic", exact: true }).click();
    await drag(
      { x: final.x + 150, y: final.y + 130 },
      { x: final.x + 230, y: final.y + 200 },
    );
    assert.notEqual(
      await previewPNG(),
      drawn,
      "mosaic must alter textured source pixels",
    );
    assert.deepEqual(
      await pixel(302, 262),
      await pixel(306, 266),
      "mosaic produces solid pixel blocks",
    );
    await page.getByRole("button", { name: "Undo", exact: true }).click();
    assert.equal(
      await previewPNG(),
      drawn,
      "button undo restores previous annotations",
    );
    await drag(
      { x: final.x + 150, y: final.y + 130 },
      { x: final.x + 230, y: final.y + 200 },
    );
    await page.screenshot({ path: join(output, `${engine}-editor.png`) });
    const expectedPNG = await previewPNG();
    await page.evaluate(() => {
      window.failNextCapture = true;
    });
    await page.getByRole("button", { name: "Capture", exact: true }).click();
    await page.getByRole("alert").waitFor();
    assert.equal(
      await page.locator(".editable").count(),
      1,
      "save failure keeps editor open for retry",
    );
    assert.equal(await previewPNG(), expectedPNG);
    await page.getByRole("button", { name: "Capture", exact: true }).click();
    await waitExport();
    assert.deepEqual(await page.evaluate(() => window.captureRequests.at(-1)), {
      token: "fixture",
      action: "complete",
    });
    assert.deepEqual(await exportSize(), {
      width: final.width * 2,
      height: final.height * 2,
    });
    const exportedPixels = await page.evaluate(async () => {
      const image = new Image();
      image.src = "data:image/png;base64," + window.captureExports.at(-1);
      await image.decode();
      const canvas = document.createElement("canvas");
      canvas.width = image.naturalWidth;
      canvas.height = image.naturalHeight;
      const ctx = canvas.getContext("2d");
      ctx.drawImage(image, 0, 0);
      return Array.from(
        new Uint8Array(
          await crypto.subtle.digest(
            "SHA-256",
            ctx.getImageData(0, 0, canvas.width, canvas.height).data,
          ),
        ),
      ).join(",");
    });
    assert.equal(
      exportedPixels,
      expectedPNG,
      "export pixels exactly match preview, without frame or toolbar",
    );
    assert.equal(
      await page.evaluate(async () => {
        const { screenshotPNG } = await import("/src/screenshot-editor.ts");
        const canvas = document.createElement("canvas");
        canvas.width = 1600;
        canvas.height = 1200;
        const ctx = canvas.getContext("2d");
        const pixels = ctx.createImageData(canvas.width, canvas.height);
        for (let offset = 0; offset < pixels.data.length; offset += 65536)
          crypto.getRandomValues(pixels.data.subarray(offset, offset + 65536));
        for (let offset = 3; offset < pixels.data.length; offset += 4)
          pixels.data[offset] = 255;
        ctx.putImageData(pixels, 0, 0);
        return screenshotPNG(canvas).then(
          () => false,
          (error) => error.message.includes("4 MB"),
        );
      }),
      true,
      "oversized PNG exports follow the attachment limit",
    );
    await load();
    await drag({ x: 300, y: 250 }, { x: 500, y: 400 });
    await page.getByRole("button", { name: "Rectangle", exact: true }).click();
    await page.getByRole("button", { name: "Select again" }).click();
    assert.equal(await page.locator(".editable").count(), 0);
    assert.equal(
      await page.locator(".capture-toolbar").count(),
      0,
      "reset hides the editor toolbar until another selection is made",
    );
    await drag({ x: 300, y: 250 }, { x: 500, y: 400 });
    assert.equal(
      await page.locator('.capture-toolbar [aria-pressed="true"]').count(),
      0,
      "reselecting must clear the active tool",
    );
    await page.keyboard.press("Escape");
    assert.equal(
      await page.evaluate(() => window.captureRequests.at(-1).action),
      "cancel",
    );
    await load();
    await drag({ x: 970, y: 10 }, { x: 995, y: 680 });
    for (const button of await page.locator(".capture-toolbar button").all()) {
      await checkTooltip(button);
      const buttonBox = await button.boundingBox();
      const tooltipBox = await page.getByRole("tooltip").boundingBox();
      assert.ok(
        tooltipBox.y >= buttonBox.y + buttonBox.height,
        "tooltips appear below a toolbar placed at the top edge",
      );
    }
    await page.goto(
      `http://127.0.0.1:${server.httpServer.address().port}/screenshot.html?picked=1`,
    );
    await page.waitForFunction(() =>
      window.captureRequests.some((request) => request.action === "ready"),
    );
    assert.equal(
      await page.locator(".capture-toolbar").count(),
      1,
      "system-picked image opens selected and ready to edit",
    );
    await page.keyboard.press("Enter");
    await waitExport();
    assert.deepEqual(
      await exportSize(),
      { width: 601, height: 401 },
      "system-picked image retains exact source pixels at fractional editor scale",
    );
    await page.goto(
      `http://127.0.0.1:${server.httpServer.address().port}/screenshot.html?capture=pending`,
    );
    await page.waitForFunction(() => window.captureLoads === 1);
    assert.equal(await page.locator(".capture-surface").count(), 0);
    assert.equal(await page.evaluate(() => window.captureRequests.length), 0);
    await page.evaluate(() => window.releaseCapture());
    await page.waitForFunction(() =>
      window.captureRequests.some((request) => request.action === "ready"),
    );
    assert.deepEqual(await rect(), { x: 280, y: 160, width: 550, height: 380 });
    await page.goto(
      `http://127.0.0.1:${server.httpServer.address().port}/screenshot.html?capture=fail`,
    );
    await page.waitForFunction(
      () => window.captureRequests.at(-1)?.action === "cancel",
    );
    assert.equal(await page.locator(".capture-surface").count(), 0);
    assert.deepEqual(errors, []);
    reports.push({
      engine,
      windowHover: true,
      windowDimensions: true,
      windowClick: true,
      neutralTools: true,
      toolToggle: true,
      keyboardFocus: true,
      pointerFocus: true,
      transparentButtons: true,
      hoverTooltips: true,
      edgeTooltips: true,
      deferredCapture: true,
      startupFailure: true,
      systemPickedEditor: true,
      dragRegion: true,
      eightHandles: true,
      wholeEdges: true,
      move: true,
      retinaExport: true,
      rectangle: true,
      line: true,
      mosaic: true,
      undo: true,
      previewMatchesExport: true,
      saveRetry: true,
      attachmentLimit: true,
      reset: true,
      cancel: true,
    });
    await browser.close();
    browser = undefined;
  }
  await writeFile(
    join(output, "report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
  console.log(JSON.stringify(reports));
} catch (error) {
  await browser?.pages()[0]?.screenshot({ path: join(output, "failure.png") });
  throw error;
} finally {
  await browser?.close();
  for (const cleanup of cleanups.reverse()) await cleanup();
}
