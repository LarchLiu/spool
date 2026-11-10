#!/usr/bin/env bun
// Reproducible platform adaptation of the checksum-pinned TM4E core.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { pathToFileURL } from "node:url";
import { readZip, writeZip } from "../../tools/archive.mjs";
const EXPECTED =
  "d78182d6362657a745236d1a085ac2ee11a958f9c3354d8faa87e36563e16174";
const BRIDGE = "com/cloudgeek/spool/textmate/TextMatePlatform";
export function instructionLength(code, offset) {
  const op = code[offset];
  if (op === 0xaa || op === 0xab) {
    const aligned = (offset + 4) & ~3;
    return op === 0xaa
      ? aligned -
          offset +
          12 +
          (code.readInt32BE(aligned + 8) - code.readInt32BE(aligned + 4) + 1) *
            4
      : aligned - offset + 8 + code.readInt32BE(aligned + 4) * 8;
  }
  if (op === 0xc4) return code[offset + 1] === 0x84 ? 6 : 4;
  if (
    [0x10, 0x12, 0xa9, 0xbc].includes(op) ||
    (op >= 0x15 && op <= 0x19) ||
    (op >= 0x36 && op <= 0x3a)
  )
    return 2;
  if (
    [0x11, 0x13, 0x14, 0x84, 0xbb, 0xbd, 0xc0, 0xc1, 0xc6, 0xc7].includes(op) ||
    (op >= 0x99 && op <= 0xa8) ||
    (op >= 0xb2 && op <= 0xb8)
  )
    return 3;
  if (op === 0xc5) return 4;
  return [0xb9, 0xba, 0xc8, 0xc9].includes(op) ? 5 : 1;
}
function adaptEmptyCalls(tail, pool, targets, bridge) {
  function attributes(offset, count, methods) {
    for (let i = 0; i < count; i++) {
      const name = tail.readUInt16BE(offset),
        length = tail.readUInt32BE(offset + 2),
        start = offset + 6;
      if (methods && pool[name] === "Code") {
        const size = tail.readUInt32BE(start + 4),
          code = tail.subarray(start + 8, start + 8 + size);
        let at = 0;
        while (at < size) {
          const op = code[at],
            step = instructionLength(code, at);
          if (
            [0xb6, 0xb9].includes(op) &&
            targets.has(code.readUInt16BE(at + 1))
          ) {
            code[at] = 0xb8;
            code.writeUInt16BE(bridge, at + 1);
            if (op === 0xb9) code.fill(0, at + 3, at + 5);
          }
          at += step;
        }
        assert.equal(at, size, "Invalid bytecode boundary");
      }
      offset = start + length;
    }
    return offset;
  }
  let offset = 8 + tail.readUInt16BE(6) * 2;
  for (const methods of [false, true]) {
    const count = tail.readUInt16BE(offset);
    offset += 2;
    for (let i = 0; i < count; i++)
      offset = attributes(offset + 8, tail.readUInt16BE(offset + 6), methods);
  }
}
export function adapt(data) {
  assert.equal(data.readUInt32BE(0), 0xcafebabe, "Invalid Java class");
  const count = data.readUInt16BE(8),
    pool = Array(count),
    chunks = [Buffer.from(data.subarray(0, 10))];
  const sizes = {
    3: 4,
    4: 4,
    5: 8,
    6: 8,
    7: 2,
    8: 2,
    9: 4,
    10: 4,
    11: 4,
    12: 4,
    15: 3,
    16: 2,
    17: 4,
    18: 4,
    19: 2,
    20: 2,
  };
  let offset = 10;
  for (let index = 1; index < count; index++) {
    const tag = data[offset++];
    chunks.push(Buffer.from([tag]));
    if (tag === 1) {
      const size = data.readUInt16BE(offset);
      offset += 2;
      let value = data.toString("latin1", offset, offset + size);
      pool[index] = value;
      offset += size;
      value = value
        .replaceAll("java/lang/System$Logger$Level", BRIDGE + "$Level")
        .replaceAll("java/lang/System$Logger", BRIDGE + "$Logger")
        .replaceAll("java/lang/System", BRIDGE);
      if (value === String.raw`\$(\d+)|\$\{(\d+):\/(downcase|upcase)}`)
        value = String.raw`\$(\d+)|\$\{(\d+):\/(downcase|upcase)\}`;
      const encoded = Buffer.from(value, "latin1"),
        length = Buffer.alloc(2);
      length.writeUInt16BE(encoded.length);
      chunks.push(length, encoded);
    } else {
      assert.ok(sizes[tag], "Unknown class constant tag");
      if ([7, 8, 16, 19, 20].includes(tag))
        pool[index] = [tag, data.readUInt16BE(offset)];
      else if ([9, 10, 11, 12, 17, 18].includes(tag))
        pool[index] = [
          tag,
          data.readUInt16BE(offset),
          data.readUInt16BE(offset + 2),
        ];
      chunks.push(data.subarray(offset, offset + sizes[tag]));
      offset += sizes[tag];
      if ([5, 6].includes(tag)) index++;
    }
  }
  const targets = new Set();
  pool.forEach((value, index) => {
    if (!Array.isArray(value) || ![10, 11].includes(value[0])) return;
    const owner = pool[pool[value[1]][1]],
      [, name, descriptor] = pool[value[2]];
    if (
      ["java/lang/CharSequence", "java/lang/StringBuilder"].includes(owner) &&
      pool[name] === "isEmpty" &&
      pool[descriptor] === "()Z"
    )
      targets.add(index);
  });
  const tail = Buffer.from(data.subarray(offset));
  if (targets.size) {
    for (const value of [BRIDGE, "isEmpty", "(Ljava/lang/CharSequence;)Z"]) {
      const encoded = Buffer.from(value),
        header = Buffer.alloc(3);
      header[0] = 1;
      header.writeUInt16BE(encoded.length, 1);
      chunks.push(header, encoded);
    }
    for (const [tag, ...values] of [
      [7, count],
      [12, count + 1, count + 2],
      [10, count + 3, count + 4],
    ]) {
      const b = Buffer.alloc(1 + values.length * 2);
      b[0] = tag;
      values.forEach((v, i) => b.writeUInt16BE(v, 1 + i * 2));
      chunks.push(b);
    }
    chunks[0].writeUInt16BE(count + 6, 8);
    adaptEmptyCalls(tail, pool, targets, count + 5);
  }
  return Buffer.concat([...chunks, tail]);
}
export function prepare(source, target) {
  const bytes = readFileSync(source);
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    EXPECTED,
    "Unexpected TM4E binary; review the adaptation before upgrading",
  );
  const output = new Map();
  for (const [name, entry] of readZip(bytes)) {
    if (
      name.endsWith(".class") &&
      ["OnigString", "OnigRegExp", "OnigResult"].some((replacement) =>
        name.startsWith(
          "org/eclipse/tm4e/core/internal/oniguruma/" + replacement,
        ),
      )
    )
      continue;
    if (name.startsWith("META-INF/") && /\.(SF|RSA|DSA)$/.test(name)) continue;
    output.set(
      name,
      name.endsWith(".class") ? adapt(entry.read()) : entry.read(),
    );
  }
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, writeZip(output));
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  assert.equal(
    process.argv.length,
    4,
    "Usage: bun prepare-textmate.mjs SOURCE TARGET",
  );
  prepare(...process.argv.slice(2));
}
