#!/usr/bin/env bun
// Small host-Java preparation steps used by check-java.sh.
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { delimiter, join } from "node:path";
import { readZip } from "../../tools/archive.mjs";
import { command } from "./common.mjs";
const [mode, ...args] = process.argv.slice(2);
if (mode === "manifest") {
  const [root, scratch] = args;
  let xml = readFileSync(
    join(root, "android/app/src/main/AndroidManifest.xml"),
    "utf8",
  );
  assert.match(xml, /<manifest\b/);
  xml = xml.replace(
    /<manifest\b([^>]*)>/,
    (_, attrs) =>
      "<manifest" +
      attrs.replace(/\s+package\s*=\s*(["'])[^"']*\1/, "") +
      ' package="com.cloudgeek.spool.android">',
  );
  writeFileSync(join(scratch, "AndroidManifest.xml"), xml);
} else if (mode === "classpath") {
  const [manifest, scratch] = args;
  const artifacts = JSON.parse(readFileSync(manifest, "utf8"));
  assert.ok(
    Array.isArray(artifacts) && artifacts.length,
    "No Gradle dependencies resolved",
  );
  const target = join(scratch, "java-dependencies"),
    jars = [];
  mkdirSync(target, { recursive: true });
  for (const [index, artifact] of artifacts.entries()) {
    if (artifact.endsWith(".jar")) jars.push(artifact);
    else if (artifact.endsWith(".aar")) {
      for (const [name, entry] of readZip(readFileSync(artifact))) {
        if (!/^(?:classes\.jar|libs\/[^/]+\.jar)$/.test(name)) continue;
        const output = join(target, `${index}-${jars.length}.jar`);
        writeFileSync(output, entry.read());
        jars.push(output);
      }
    } else throw new Error("Unsupported Java dependency: " + artifact);
  }
  console.log(jars.join(delimiter));
} else if (mode === "compile") {
  const [javac, cp, scratch, root] = args,
    folders = [
      join(scratch, "generated/java"),
      join(scratch, "r"),
      join(root, "android/app/src/main/java"),
      join(root, "android/app/src/test/java"),
      join(root, "android/textmate/src/main/java"),
    ];
  const sources = folders.flatMap((cwd) => [
    ...new Bun.Glob("**/*.java").scanSync({ cwd, absolute: true }),
  ]);
  command(
    javac,
    [
      "-encoding",
      "UTF-8",
      "-source",
      "17",
      "-target",
      "17",
      "-Xlint:-options",
      "-cp",
      cp,
      "-d",
      join(scratch, "classes"),
      ...sources,
    ],
    { stdio: "inherit", timeout: 180000 },
  );
} else throw new Error("Expected manifest, classpath, or compile");
