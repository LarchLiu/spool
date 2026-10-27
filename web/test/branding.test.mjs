import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

test("desktop and PWA metadata consistently use Spool", async () => {
  const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
  const desktop = JSON.parse(await read("../../mygo.json"));
  const manifest = JSON.parse(await read("../public/manifest.webmanifest"));
  const pkg = JSON.parse(await read("../package.json"));
  assert.equal(desktop.name, "Spool");
  assert.equal(desktop.slug, "spool");
  assert.equal(desktop.identifier, "com.cloudgeek.spool");
  assert.equal(desktop.icon, "assets/icon.png");
  assert.match(desktop.buildCommand, /go run \.\/tools\/branding\.go/);
  assert.equal(manifest.name, "Spool");
  assert.equal(manifest.short_name, "Spool");
  assert.equal(pkg.name, "spool-web");
  assert.match(await read("../index.html"), /<title>Spool · Chat<\/title>/);
  assert.match(await read("../index.html"), /href="\/favicon\.png"/);
  assert.match(await read("../index.html"), /href="\/apple-touch-icon\.png"/);
  assert.ok(manifest.icons.some((icon) => icon.purpose === "maskable"));
});

test("logo-derived icons are exported at the expected dimensions", async () => {
  for (const [path, size] of [
    ["../../assets/icon.png", 1024],
    ["../../tray.png", 44],
    ["../public/icon-192.png", 192],
    ["../public/icon-512.png", 512],
    ["../public/icon-maskable-512.png", 512],
    ["../public/apple-touch-icon.png", 180],
    ["../public/favicon.png", 32],
    ["../public/logo.png", 128],
  ]) {
    const png = await readFile(new URL(path, import.meta.url));
    assert.equal(png.subarray(1, 4).toString(), "PNG", path);
    assert.equal(png.readUInt32BE(16), size, path);
    assert.equal(png.readUInt32BE(20), size, path);
  }
});
