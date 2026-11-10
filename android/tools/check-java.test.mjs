import assert from "node:assert/strict";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { delimiter, join, sep } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { writeZip } from "../../tools/archive.mjs";
import { createScratch } from "../../tools/test-scratch.mjs";
import { command } from "./common.mjs";

const script = fileURLToPath(new URL("./check-java.mjs", import.meta.url));

test("host Java classpath keeps JARs and extracts all AAR classes without resources", async (t) => {
  const root = await createScratch("java-classpath-test");
  t.after(() => rm(root, { recursive: true, force: true }));
  const folder = join(root, "dependencies with spaces");
  await mkdir(folder);
  const direct = join(folder, "direct.jar"),
    aar = join(folder, "ui.aar");
  const classes = writeZip(
    new Map([["fixture/UI.class", Buffer.from("ui bytecode")]]),
  );
  const embedded = writeZip(
    new Map([["fixture/Helper.class", Buffer.from("helper bytecode")]]),
  );
  await writeFile(
    direct,
    writeZip(new Map([["fixture/Core.class", Buffer.from("core bytecode")]])),
  );
  await writeFile(
    aar,
    writeZip(
      new Map([
        ["classes.jar", classes],
        ["libs/helper.jar", embedded],
        ["res/raw/payload.jar", Buffer.from("not on the classpath")],
        ["AndroidManifest.xml", Buffer.from("<manifest/>")],
      ]),
    ),
  );
  const manifest = join(root, "dependencies.json");
  await writeFile(manifest, JSON.stringify([direct, aar]));
  const result = command(process.execPath, [
    script,
    "classpath",
    manifest,
    root,
  ]);
  const jars = result.stdout.trim().split(delimiter);
  assert.equal(jars.length, 3);
  assert.equal(jars[0], direct);
  const extracted = await Promise.all(
    jars.slice(1).map((file) => readFile(file)),
  );
  assert.ok(extracted.some((bytes) => bytes.equals(classes)));
  assert.ok(extracted.some((bytes) => bytes.equals(embedded)));
  for (const path of jars.slice(1))
    assert.equal(path.startsWith(join(root, "java-dependencies") + sep), true);
});
