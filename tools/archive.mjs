// Small deterministic ZIP reader/writer for JAR/APK tooling; no package install needed.
import { deflateRawSync, inflateRawSync } from "node:zlib";
import assert from "node:assert/strict";

const crcTable = Uint32Array.from({ length: 256 }, (_, value) => {
  for (let bit = 0; bit < 8; bit++)
    value = (value >>> 1) ^ (0xedb88320 & -(value & 1));
  return value >>> 0;
});
export function crc32(data) {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc = (crc >>> 8) ^ crcTable[(crc ^ byte) & 255];
  }
  return (crc ^ 0xffffffff) >>> 0;
}
export function readZip(input) {
  const data = Buffer.from(input),
    entries = new Map();
  let end = data.length - 22;
  for (; end >= Math.max(0, data.length - 65557); end--) {
    if (
      data.readUInt32LE(end) === 0x06054b50 &&
      end + 22 + data.readUInt16LE(end + 20) === data.length
    )
      break;
  }
  assert.ok(
    end >= 0 && data.readUInt32LE(end) === 0x06054b50,
    "Invalid ZIP directory",
  );
  assert.equal(data.readUInt16LE(end + 4), 0, "Split ZIP is unsupported");
  const count = data.readUInt16LE(end + 10);
  assert.notEqual(count, 0xffff, "ZIP64 is unsupported");
  let at = data.readUInt32LE(end + 16);
  for (let i = 0; i < count; i++) {
    assert.equal(data.readUInt32LE(at), 0x02014b50, "Invalid ZIP entry");
    const flags = data.readUInt16LE(at + 8),
      method = data.readUInt16LE(at + 10);
    const checksum = data.readUInt32LE(at + 16),
      compressedSize = data.readUInt32LE(at + 20),
      size = data.readUInt32LE(at + 24);
    const nameSize = data.readUInt16LE(at + 28),
      extra = data.readUInt16LE(at + 30),
      comment = data.readUInt16LE(at + 32);
    const name = data.toString("utf8", at + 46, at + 46 + nameSize),
      local = data.readUInt32LE(at + 42);
    assert.ok(
      !(flags & 1) && [0, 8].includes(method),
      "Unsupported ZIP compression/encryption",
    );
    assert.equal(
      data.readUInt32LE(local),
      0x04034b50,
      "Invalid ZIP local entry",
    );
    const start =
      local +
      30 +
      data.readUInt16LE(local + 26) +
      data.readUInt16LE(local + 28);
    assert.ok(
      start + compressedSize <= data.length && size <= 1024 ** 3,
      "Invalid ZIP size",
    );
    assert.ok(!entries.has(name), "Duplicate ZIP entry: " + name);
    let value;
    entries.set(name, {
      name,
      size,
      compressedSize,
      read() {
        if (!value) {
          const raw = data.subarray(start, start + compressedSize);
          value =
            method === 0
              ? Buffer.from(raw)
              : inflateRawSync(raw, { maxOutputLength: Math.max(1, size) });
          assert.equal(value.length, size, "ZIP size mismatch");
          assert.equal(crc32(value), checksum, "ZIP checksum mismatch");
        }
        return value;
      },
    });
    at += 46 + nameSize + extra + comment;
  }
  return entries;
}
export function writeZip(entries) {
  const files = [],
    central = [];
  let offset = 0;
  for (const [name, input] of [...entries].sort(([a], [b]) =>
    a < b ? -1 : a > b ? 1 : 0,
  )) {
    const data = Buffer.from(input),
      encoded = Buffer.from(name),
      compressed = deflateRawSync(data),
      checksum = crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x800, 6);
    local.writeUInt16LE(8, 8);
    local.writeUInt16LE(33, 12);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(compressed.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(encoded.length, 26);
    const record = Buffer.alloc(46);
    record.writeUInt32LE(0x02014b50);
    record.writeUInt16LE(20, 4);
    record.writeUInt16LE(20, 6);
    record.writeUInt16LE(0x800, 8);
    record.writeUInt16LE(8, 10);
    record.writeUInt16LE(33, 14);
    record.writeUInt32LE(checksum, 16);
    record.writeUInt32LE(compressed.length, 20);
    record.writeUInt32LE(data.length, 24);
    record.writeUInt16LE(encoded.length, 28);
    record.writeUInt32LE(offset, 42);
    files.push(local, encoded, compressed);
    central.push(record, encoded);
    offset += local.length + encoded.length + compressed.length;
  }
  const directory = Buffer.concat(central),
    end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(entries.size, 8);
  end.writeUInt16LE(entries.size, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...files, directory, end]);
}
