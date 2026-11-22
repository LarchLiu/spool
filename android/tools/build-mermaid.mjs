#!/usr/bin/env bun
// Generate the offline Android document from the same library and frame as Web.
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { mermaidDocument } from "../../web/src/mermaid-document.ts";

const require = createRequire(
  new URL("../../web/package.json", import.meta.url),
);
const output = new URL(
  "../../build/android/mermaid/assets/mermaid/",
  import.meta.url,
);
const library = await readFile(
  require.resolve("mermaid/dist/mermaid.min.js"),
  "utf8",
);
const renderer = await readFile(
  new URL("../../web/src/mermaid-frame.js", import.meta.url),
  "utf8",
);
await mkdir(output, { recursive: true });
await writeFile(
  new URL("index.html", output),
  mermaidDocument([{ code: library }, { code: renderer }]),
);
await writeFile(
  new URL("LICENSE", output),
  await readFile(require.resolve("mermaid/LICENSE")),
);
console.log("Prepared offline Mermaid assets: " + fileURLToPath(output));
