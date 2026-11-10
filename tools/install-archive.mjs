// Inlined into the standalone installer; validate before any application changes.
import { createHash } from "node:crypto";
import {
  createReadStream,
  readFileSync,
  writeFileSync,
  statSync,
} from "node:fs";
import { join } from "node:path";
import { extractTarGz } from "./tar.mjs";
const [mode, ...args] = process.argv.slice(2);
if (mode === "manifest") {
  const [work, repo, target] = args,
    raw = readFileSync(join(work, "manifest.json"));
  if (raw.length > 1048576) throw new Error("manifest exceeds size limit");
  const manifest = JSON.parse(raw),
    version = manifest.version;
  if (
    typeof version !== "string" ||
    !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/.test(
      version,
    )
  )
    throw new Error("invalid release version");
  const url = `https://github.com/${repo}/releases/download/v${version}/spool-${version}-${target}.tar.gz`;
  if (
    manifest.schema !== "spool-update-v1" ||
    manifest.repository !== repo ||
    manifest.target !== target ||
    manifest.url !== url
  )
    throw new Error("manifest identity does not match this installer");
  if (
    !Number.isSafeInteger(manifest.size) ||
    manifest.size <= 0 ||
    manifest.size > 1073741824
  )
    throw new Error("invalid archive size");
  if (
    typeof manifest.signature !== "string" ||
    !/^[A-Za-z0-9+/]{86}==$/.test(manifest.signature)
  )
    throw new Error("invalid archive signature");
  const signature = Buffer.from(manifest.signature, "base64");
  if (
    signature.length !== 64 ||
    signature.toString("base64") !== manifest.signature
  )
    throw new Error("invalid archive signature");
  writeFileSync(join(work, "archive.sig"), signature);
  writeFileSync(join(work, "url"), url);
  console.log(`Verified Spool ${version} for ${target}`);
} else if (mode === "digest") {
  const [work, archive] = args;
  if (
    statSync(archive).size !==
    JSON.parse(readFileSync(join(work, "manifest.json"), "utf8")).size
  )
    throw new Error("archive size mismatch");
  const digest = createHash("sha256");
  for await (const chunk of createReadStream(archive)) digest.update(chunk);
  writeFileSync(join(work, "digest"), digest.digest());
} else if (mode === "extract") extractTarGz(readFileSync(args[0]), args[1]);
else throw new Error("Unknown installer archive command");
