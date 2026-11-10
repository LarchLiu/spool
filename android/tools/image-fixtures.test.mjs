import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { join } from "node:path";
import { readFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { ROOT } from "./common.mjs";
const require = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(ROOT, "web/node_modules"),
    "__image_test__.cjs",
  ),
);
let chromium;
try {
  ({ chromium } = require("playwright"));
} catch {}
test(
  "Bun-generated animations match the original browser frame, timing, loop, and orientation oracle",
  { skip: !chromium || !Bun.which("cwebp"), timeout: 30000 },
  async () => {
    const output = mkdtempSync(join(tmpdir(), "image-fixtures-test."));
    let server, browser;
    try {
      const generated = spawnSync(
        process.execPath,
        [
          join(ROOT, "android/tools/image-animation-fixtures.mjs"),
          "--output",
          output,
        ],
        { encoding: "utf8" },
      );
      assert.equal(generated.status, 0, generated.stderr);
      server = Bun.serve({
        hostname: "127.0.0.1",
        port: 0,
        fetch: () =>
          new Response(
            "<!doctype html><title>Synthetic image validation</title>",
            { headers: { "Content-Type": "text/html" } },
          ),
      });
      browser = await chromium.launch({ channel: "chrome", headless: true });
      const page = await browser.newPage();
      await page.goto(server.url.href);
      const expected = JSON.parse(
        readFileSync(
          join(
            ROOT,
            "android/app/src/test/resources/image-animation/web-frames.json",
          ),
          "utf8",
        ),
      );
      for (const sample of expected) {
        const actual = await page.evaluate(
          async ({ name, data }) => {
            const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0)),
              type = name.endsWith(".gif") ? "image/gif" : "image/webp",
              decoder = new ImageDecoder({ data: bytes, type });
            try {
              await decoder.tracks.ready;
              const track = decoder.tracks.selectedTrack,
                frames = [];
              for (let i = 0; i < track.frameCount; i++) {
                const { image } = await decoder.decode({
                  frameIndex: i,
                  completeFramesOnly: true,
                });
                try {
                  const pixels = new Uint8Array(
                    image.allocationSize({ format: "RGBA" }),
                  );
                  await image.copyTo(pixels, { format: "RGBA" });
                  frames.push({
                    width: image.displayWidth,
                    height: image.displayHeight,
                    duration: image.duration / 1000,
                    pixels: btoa(String.fromCharCode(...pixels)),
                  });
                } finally {
                  image.close();
                }
              }
              const bitmap = await createImageBitmap(
                  new Blob([bytes], { type }),
                  { imageOrientation: "from-image" },
                ),
                canvas = new OffscreenCanvas(bitmap.width, bitmap.height),
                context = canvas.getContext("2d");
              context.drawImage(bitmap, 0, 0);
              const poster = {
                width: bitmap.width,
                height: bitmap.height,
                pixels: btoa(
                  String.fromCharCode(
                    ...context.getImageData(0, 0, bitmap.width, bitmap.height)
                      .data,
                  ),
                ),
              };
              bitmap.close();
              const element = new Image();
              element.src = `data:${type};base64,${data}`;
              await element.decode();
              poster.elementWidth = element.naturalWidth;
              poster.elementHeight = element.naturalHeight;
              return {
                name,
                loops: Number.isFinite(track.repetitionCount)
                  ? track.repetitionCount + 1
                  : 0,
                frames,
                poster,
              };
            } finally {
              decoder.close();
            }
          },
          {
            name: sample.name,
            data: readFileSync(join(output, sample.name)).toString("base64"),
          },
        );
        assert.deepEqual(
          actual,
          sample,
          sample.name + " differs from the original browser oracle",
        );
      }
    } finally {
      await browser?.close();
      server?.stop(true);
      rmSync(output, { recursive: true, force: true });
    }
  },
);
