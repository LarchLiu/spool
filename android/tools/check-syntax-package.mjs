#!/usr/bin/env bun
// Inspect shipped syntax assets, native ABIs, and API-26 backports in an APK.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { readZip } from "../../tools/archive.mjs";
import { ROOT, cli, writeJson } from "./common.mjs";
const forbidden = new Set([
  "Ljava/lang/CharSequence;:isEmpty",
  "Ljava/lang/StringBuilder;:isEmpty",
  "Ljava/lang/String;:isBlank",
  "Ljava/lang/String;:repeat",
  "Ljava/lang/System;:getLogger",
  "Ljava/util/stream/Stream;:toList",
]);
export function dexMethods(data) {
  assert.equal(data.toString("ascii", 0, 4), "dex\n", "Not a DEX file");
  const strings = [],
    count = data.readUInt32LE(0x38),
    offset = data.readUInt32LE(0x3c);
  for (let i = 0; i < count; i++) {
    let at = data.readUInt32LE(offset + i * 4);
    while (data[at] & 128) at++;
    at++;
    const end = data.indexOf(0, at);
    assert.ok(end >= at, "Unterminated DEX string");
    strings.push(data.toString("utf8", at, end));
  }
  assert.ok(
    !strings.includes(String.raw`\$(\d+)|\$\{(\d+):\/(downcase|upcase)}`),
    "TextMate RegexSource still contains the ICU-incompatible capture pattern",
  );
  const types = Array.from(
    { length: data.readUInt32LE(0x40) },
    (_, i) => strings[data.readUInt32LE(data.readUInt32LE(0x44) + i * 4)],
  );
  assert.ok(
    !types.some((name) => /^(Lorg\/joni\/|Lorg\/jcodings\/)/.test(name)),
    "Unused Joni/Jcodings backend was packaged",
  );
  const methods = new Set(),
    methodOffset = data.readUInt32LE(0x5c);
  for (let i = 0; i < data.readUInt32LE(0x58); i++) {
    const at = methodOffset + i * 8;
    methods.add(
      types[data.readUInt16LE(at)] + ":" + strings[data.readUInt32LE(at + 4)],
    );
  }
  return methods;
}
export function checkElf(data, machine) {
  assert.ok(
    data.subarray(0, 6).equals(Buffer.from([127, 69, 76, 70, 2, 1])) &&
      data.readUInt16LE(18) === machine,
    "Unexpected native ELF format/ABI",
  );
  const offset = Number(data.readBigUInt64LE(32)),
    size = data.readUInt16LE(54),
    count = data.readUInt16LE(56),
    loads = [];
  for (let i = 0; i < count; i++)
    if (data.readUInt32LE(offset + i * size) === 1)
      loads.push(Number(data.readBigUInt64LE(offset + i * size + 48)));
  assert.ok(
    loads.length && Math.min(...loads) >= 16384,
    "Syntax native library lacks 16 KiB PT_LOAD alignment",
  );
  for (const method of ["nCompile", "nSearch", "nClose"])
    assert.ok(
      data.includes(
        Buffer.from(
          "Java_com_cloudgeek_spool_textmate_NativeOniguruma_" + method,
        ),
      ),
      "Missing syntax JNI export: " + method,
    );
  return loads;
}
export function checkPackage(apkPath, requestedAbis = ["arm64-v8a"]) {
  const bytes = readFileSync(apkPath),
    apk = readZip(bytes),
    names = [...apk.keys()];
  const assets = join(ROOT, "android/app/src/main/assets/syntax"),
    expected = new Map();
  for (const file of new Bun.Glob("**/*").scanSync({
    cwd: assets,
    onlyFiles: true,
  }))
    expected.set("assets/syntax/" + file, join(assets, file));
  assert.deepEqual(
    new Set(names.filter((name) => name.startsWith("assets/syntax/"))),
    new Set(expected.keys()),
    "Missing or stale packaged syntax assets",
  );
  for (const [name, source] of expected)
    assert.ok(
      apk.get(name).read().equals(readFileSync(source)),
      "Packaged asset drift: " + name,
    );
  const packagedAbis = new Set(
    names
      .filter((name) => name.startsWith("lib/") && name.endsWith(".so"))
      .map((name) => name.split("/")[1]),
  );
  assert.deepEqual(
    packagedAbis,
    new Set(requestedAbis),
    "Unexpected packaged ABIs",
  );
  const alignments = {};
  for (const abi of requestedAbis) {
    assert.ok(["arm64-v8a", "x86_64"].includes(abi), "Invalid ABI");
    alignments[abi] = checkElf(
      apk.get(`lib/${abi}/libspool-textmate.so`).read(),
      abi === "arm64-v8a" ? 183 : 62,
    );
  }
  const dexes = names.filter((name) => name.endsWith(".dex")),
    methods = new Set();
  for (const name of dexes)
    for (const method of dexMethods(apk.get(name).read())) methods.add(method);
  for (const name of ["nCompile", "nSearch", "nClose"])
    assert.ok(
      methods.has("Lcom/cloudgeek/spool/textmate/NativeOniguruma;:" + name),
      "Syntax JNI names were removed or renamed: " + name,
    );
  const unresolved = [...methods]
    .filter((method) => forbidden.has(method))
    .sort()
    .map((method) => method.split(":"));
  assert.equal(
    unresolved.length,
    0,
    "Methods unavailable on API 26 remain in DEX: " +
      JSON.stringify(unresolved),
  );
  assert.ok(
    apk
      .get("assets/third-party-notices.txt")
      .read()
      .equals(readFileSync(join(ROOT, "android/THIRD_PARTY_NOTICES"))),
    "Packaged third-party notice drift",
  );
  return {
    apkSHA256: createHash("sha256").update(bytes).digest("hex"),
    syntaxAssets: expected.size,
    syntaxBytes: [...expected.keys()].reduce(
      (sum, key) => sum + apk.get(key).size,
      0,
    ),
    syntaxCompressedBytes: [...expected.keys()].reduce(
      (sum, key) => sum + apk.get(key).compressedSize,
      0,
    ),
    nativeLoadAlignments: alignments,
    syntaxJniMethodsPreserved: true,
    dexFiles: dexes.length,
    unresolvedCheckedAPI26Methods: unresolved,
    joniOrJcodingsRuntime: false,
    thirdPartyNoticesMatch: true,
  };
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { values } = cli({
    apk: {
      type: "string",
      default: join(
        ROOT,
        "build/android/gradle/app/outputs/apk/debug/app-debug.apk",
      ),
    },
    abis: { type: "string", multiple: true },
    report: { type: "string" },
  });
  const report = checkPackage(values.apk, values.abis);
  if (values.report) writeJson(values.report, report);
  console.log(JSON.stringify(report, null, 2));
}
