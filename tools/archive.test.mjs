import assert from "node:assert/strict";
import test from "node:test";
import { spawnSync } from "node:child_process";
import { adapt } from "../android/tools/prepare-textmate.mjs";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { gzipSync } from "node:zlib";
import { readZip, writeZip } from "./archive.mjs";
import { extractTarGz } from "./tar.mjs";
import { standaloneFace } from "../android/tools/prepare-ui-fonts.mjs";
import {
  checkElf,
  dexMethods,
} from "../android/tools/check-syntax-package.mjs";
function scratch(t) {
  const dir = mkdtempSync(join(tmpdir(), "archive-test-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}
test("ZIP entries round-trip deterministically with CRC validation and reject tampering", () => {
  const inputs = new Map([
      ["z/empty", Buffer.alloc(0)],
      ["a/class", Buffer.from("synthetic class bytes".repeat(30))],
    ]),
    bytes = writeZip(inputs),
    entries = readZip(bytes);
  assert.deepEqual(bytes, writeZip(inputs));
  assert.deepEqual([...entries.keys()], ["a/class", "z/empty"]);
  for (const [name, expected] of inputs)
    assert.deepEqual(entries.get(name).read(), expected);
  const broken = Buffer.from(bytes),
    central = broken.indexOf(Buffer.from("504b0102", "hex"));
  broken[central + 16] ^= 1;
  assert.throws(() => readZip(broken).get("a/class").read(), /checksum/);
});
function tar(name, type = "0", payload = Buffer.from("safe"), mode = 0o644) {
  const header = Buffer.alloc(512),
    octal = (at, width, value) =>
      header.write(
        value.toString(8).padStart(width - 1, "0") + "\0",
        at,
        width,
      );
  header.write(name);
  octal(100, 8, mode);
  octal(124, 12, payload.length);
  header.fill(32, 148, 156);
  header.write(type, 156);
  header.write(
    [...header]
      .reduce((sum, byte) => sum + byte, 0)
      .toString(8)
      .padStart(6, "0") + "\0 ",
    148,
    8,
  );
  return gzipSync(
    Buffer.concat([
      header,
      payload,
      Buffer.alloc((512 - (payload.length % 512)) % 512),
      Buffer.alloc(1024),
    ]),
  );
}
test("tar rejects unsafe entries, expansion beyond the budget, and duplicate extraction", (t) => {
  const root = scratch(t);
  for (const name of ["/absolute", "../escape", "dir/../../escape"])
    assert.throws(() => extractTarGz(tar(name), root), /unsafe entry/);
  for (const type of ["1", "2", "3", "6"])
    assert.throws(() => extractTarGz(tar("link", type), root), /unsafe entry/);
  assert.throws(() => extractTarGz(tar("big"), root, 2), /size limit/);
  extractTarGz(tar("a/file"), root);
  assert.equal(readFileSync(join(root, "a/file"), "utf8"), "safe");
  assert.throws(() => extractTarGz(tar("a/file"), root), /EEXIST/);
});
test("TTC face extraction preserves tables and creates the required SFNT checksum", () => {
  const collection = Buffer.alloc(128);
  collection.write("ttcf");
  collection.writeUInt32BE(1, 8);
  collection.writeUInt32BE(16, 12);
  collection.writeUInt32BE(0x10000, 16);
  collection.writeUInt16BE(2, 20);
  collection.write("head", 28);
  collection.writeUInt32BE(80, 36);
  collection.writeUInt32BE(16, 40);
  collection.write("test", 44);
  collection.writeUInt32BE(100, 52);
  collection.writeUInt32BE(3, 56);
  collection.write("abc", 100);
  const result = standaloneFace(collection, 0);
  assert.equal(result.readUInt16BE(4), 2);
  const payload = result.readUInt32BE(36);
  assert.equal(result.toString("ascii", payload, payload + 3), "abc");
  let checksum = 0;
  for (let at = 0; at < result.length; at += 4)
    checksum = (checksum + result.readUInt32BE(at)) >>> 0;
  assert.equal(checksum, 0xb1b0afba);
});
test("ELF syntax check enforces ABI, 16KiB alignment, and all JNI exports", () => {
  const bytes = Buffer.alloc(512);
  Buffer.from([127, 69, 76, 70, 2, 1]).copy(bytes);
  bytes.writeUInt16LE(183, 18);
  bytes.writeBigUInt64LE(64n, 32);
  bytes.writeUInt16LE(56, 54);
  bytes.writeUInt16LE(1, 56);
  bytes.writeUInt32LE(1, 64);
  bytes.writeBigUInt64LE(16384n, 112);
  bytes.write(
    ["nCompile", "nSearch", "nClose"]
      .map(
        (name) => "Java_com_cloudgeek_spool_textmate_NativeOniguruma_" + name,
      )
      .join("\0"),
    128,
  );
  assert.deepEqual(checkElf(bytes, 183), [16384]);
  assert.throws(() => checkElf(bytes, 62), /ABI/);
  bytes.writeBigUInt64LE(4096n, 112);
  assert.throws(() => checkElf(bytes, 183), /16 KiB/);
  assert.throws(() => dexMethods(Buffer.alloc(120)), /DEX/);
});

test("adapted Java bytecode keeps offsets valid and calls the Android compatibility bridge", (t) => {
  const javaHome = [
    process.env.JAVA_HOME,
    "/Applications/Android Studio.app/Contents/jbr/Contents/Home",
  ].find(
    (home) =>
      home &&
      existsSync(join(home, "bin/javac")) &&
      existsSync(join(home, "bin/java")),
  );
  const javac = javaHome ? join(javaHome, "bin/javac") : Bun.which("javac");
  const java = javaHome ? join(javaHome, "bin/java") : Bun.which("java");
  const version = javac && spawnSync(javac, ["-version"], { encoding: "utf8" });
  const major = version && /javac (\d+)/.exec(version.stdout + version.stderr);
  if (!java || !major || version.status !== 0 || Number(major[1]) < 17) {
    t.skip(
      "Android bytecode integration requires an installed JDK 17 or later",
    );
    return;
  }
  const root = scratch(t);
  const source = `public class CompatibilityProbe {
    static boolean empty(CharSequence text) { return text.isEmpty(); }
    static boolean builder(StringBuilder text) { return text.isEmpty(); }
    static int lookup(int n) { switch(n) { case 2: return 1; case 100: return 2; default: return 0; } }
    static int table(int n) { switch(n) { case 1: return 1; case 2: return 2; case 3: return 3; default: return 0; } }
    public static void main(String[] args) {
      if (!empty("") || empty("text") || !builder(new StringBuilder()) || builder(new StringBuilder("text")) || lookup(100) != 2 || table(2) != 2) throw new AssertionError();
      System.getLogger("compatibility").log(System.Logger.Level.INFO, "ok");
    }
  }`;
  writeFileSync(join(root, "CompatibilityProbe.java"), source);
  const compile = spawnSync(
    javac,
    [
      "-d",
      root,
      join(root, "CompatibilityProbe.java"),
      "android/textmate/src/main/java/com/cloudgeek/spool/textmate/TextMatePlatform.java",
    ],
    { encoding: "utf8" },
  );
  assert.equal(compile.status, 0, compile.stderr);
  const file = join(root, "CompatibilityProbe.class"),
    data = readFileSync(file),
    adapted = adapt(data);
  assert.ok(!adapted.includes(Buffer.from("java/lang/System$Logger")));
  writeFileSync(file, adapted);
  const result = spawnSync(
    java,
    ["-Xverify:all", "-cp", root, "CompatibilityProbe"],
    { encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stderr, /ok/);
});
