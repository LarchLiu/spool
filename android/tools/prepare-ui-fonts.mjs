#!/usr/bin/env bun
// Use identical installed Robolectric font bytes for native and browser checks.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";
import { pathToFileURL } from "node:url";
import { readZip } from "../../tools/archive.mjs";
import { ROOT, writeJson, cli } from "./common.mjs";
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
export function standaloneFace(collection, index) {
  const base = collection.readUInt32BE(12 + index * 4),
    tables = [];
  for (let i = 0; i < collection.readUInt16BE(base + 4); i++) {
    const at = base + 12 + i * 16,
      tag = collection.toString("ascii", at, at + 4);
    if (tag === "DSIG") continue;
    const offset = collection.readUInt32BE(at + 8),
      size = collection.readUInt32BE(at + 12),
      bytes = Buffer.from(collection.subarray(offset, offset + size));
    if (tag === "head") bytes.fill(0, 8, 12);
    tables.push([tag, bytes]);
  }
  tables.sort(([a], [b]) => (a < b ? -1 : 1));
  const count = tables.length,
    selector = Math.floor(Math.log2(count)),
    header = Buffer.alloc(12 + count * 16),
    body = [];
  collection.copy(header, 0, base, base + 4);
  header.writeUInt16BE(count, 4);
  header.writeUInt16BE(16 * 2 ** selector, 6);
  header.writeUInt16BE(selector, 8);
  header.writeUInt16BE(count * 16 - 16 * 2 ** selector, 10);
  const checksum = (bytes) => {
    let sum = 0;
    for (let at = 0; at < bytes.length; at += 4)
      sum = (sum + bytes.readUInt32BE(at)) >>> 0;
    return sum;
  };
  let offset = header.length,
    head;
  tables.forEach(([tag, bytes], i) => {
    const padded = Buffer.alloc(Math.ceil(bytes.length / 4) * 4);
    bytes.copy(padded);
    const at = 12 + i * 16;
    header.write(tag, at, "ascii");
    header.writeUInt32BE(checksum(padded), at + 4);
    header.writeUInt32BE(offset, at + 8);
    header.writeUInt32BE(bytes.length, at + 12);
    if (tag === "head") head = offset;
    body.push(padded);
    offset += padded.length;
  });
  const result = Buffer.concat([header, ...body]);
  if (head !== undefined)
    result.writeUInt32BE((0xb1b0afba - checksum(result)) >>> 0, head + 8);
  return result;
}
export function prepareFonts(jar, CACHE) {
  mkdirSync(CACHE, { recursive: true });
  const archive = readZip(readFileSync(jar)),
    manifest = {
      source: "org.robolectric:nativeruntime-dist-compat:1.0.18",
      faces: [],
    };
  const read = (name) => {
    const entry = archive.get("fonts/" + name);
    if (!entry) throw new Error("Missing font: " + name);
    return entry.read();
  };
  const save = (name, bytes) => {
    const target = join(CACHE, name);
    if (!existsSync(target) || !readFileSync(target).equals(bytes))
      writeFileSync(target, bytes);
  };
  for (const [weight, name] of Object.entries({
    100: "Thin",
    300: "Light",
    400: "Regular",
    500: "Medium",
    700: "Bold",
    900: "Black",
  })) {
    for (const italic of [false, true]) {
      const file =
          "Roboto-" +
          (name === "Regular" && italic
            ? "Italic"
            : name + (italic ? "Italic" : "")) +
          ".ttf",
        bytes = read(file);
      save(file, bytes);
      manifest.faces.push({
        file,
        weight: Number(weight),
        italic,
        sha256: hash(bytes),
      });
    }
  }
  const mono = read("DroidSansMono.ttf");
  save("DroidSansMono.ttf", mono);
  manifest.mono = { file: "DroidSansMono.ttf", sha256: hash(mono) };
  const collection = read("NotoSansCJK-Regular.ttc");
  manifest.cjk = [];
  for (const [locale, index, name] of [
    ["zh-CN", 2, "SC"],
    ["ja", 0, "JP"],
  ]) {
    const bytes = standaloneFace(collection, index),
      file = "NotoSansCJK-" + name + ".otf";
    save(file, bytes);
    manifest.cjk.push({
      locale,
      family: "AndroidCjk" + name,
      index,
      file,
      sha256: hash(bytes),
      sourceFile: "NotoSansCJK-Regular.ttc",
      sourceSHA256: hash(collection),
    });
  }
  manifest.fontConfigSHA256 = hash(read("fonts.xml"));
  writeJson(join(CACHE, "manifest.json"), manifest);
  console.log(
    "Prepared matching Robolectric/browser reference fonts (test assets only).",
  );
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { values } = cli({
    output: {
      type: "string",
      default: join(ROOT, "build/cache/android-ui-fonts"),
    },
  });
  const gradle = process.env.GRADLE_USER_HOME || join(homedir(), ".gradle");
  const jars = [
    ...new Bun.Glob(
      "caches/modules-2/files-2.1/org.robolectric/nativeruntime-dist-compat/1.0.18/*/*.jar",
    ).scanSync({ cwd: gradle, absolute: true }),
  ].sort();
  if (jars.length !== 1)
    throw new Error(
      "Run the pinned Gradle native UI tests first to resolve Robolectric fonts.",
    );
  prepareFonts(jars[0], values.output);
}
