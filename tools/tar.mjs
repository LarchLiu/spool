// Bounded tar extraction for verified release/source archives. Never extract links.
import assert from "node:assert/strict";
import { gunzipSync } from "node:zlib";
import { mkdirSync, writeFileSync, chmodSync, lstatSync } from "node:fs";
import { resolve, sep, dirname } from "node:path";
export function extractTarGz(input, destination, limit = 1024 ** 3) {
  const data = gunzipSync(input, { maxOutputLength: limit + 16 * 1024 * 1024 });
  const root = resolve(destination);
  let total = 0,
    at = 0,
    pending = {};
  const text = (bytes) =>
    bytes
      .subarray(0, bytes.indexOf(0) < 0 ? bytes.length : bytes.indexOf(0))
      .toString("utf8");
  const number = (bytes) => {
    assert.ok(!(bytes[0] & 0x80), "Unsupported tar number");
    const value = text(bytes).trim();
    assert.ok(/^[0-7]*$/.test(value), "Invalid tar number");
    return parseInt(value || "0", 8);
  };
  function directory(path) {
    if (path !== root) directory(dirname(path));
    mkdirSync(path, { recursive: true });
    assert.ok(
      lstatSync(path).isDirectory() && !lstatSync(path).isSymbolicLink(),
      "unsafe entry in archive",
    );
  }
  while (at + 512 <= data.length) {
    const header = data.subarray(at, at + 512);
    if (header.every((byte) => byte === 0)) break;
    const checksum = number(header.subarray(148, 156));
    let sum = 0;
    for (let i = 0; i < 512; i++) sum += i >= 148 && i < 156 ? 32 : header[i];
    assert.equal(sum, checksum, "Invalid tar checksum");
    let name = text(header.subarray(0, 100));
    const prefix = text(header.subarray(345, 500));
    if (prefix) name = prefix + "/" + name;
    const size = number(header.subarray(124, 136)),
      type = header[156],
      mode = number(header.subarray(100, 108));
    const start = at + 512;
    assert.ok(start + size <= data.length, "Truncated tar entry");
    const payload = data.subarray(start, start + size);
    at = start + Math.ceil(size / 512) * 512;
    if (type === 120 || type === 103) {
      const metadata = {};
      let cursor = 0;
      while (cursor < payload.length) {
        const space = payload.indexOf(32, cursor),
          length = Number(payload.toString("ascii", cursor, space));
        assert.ok(
          space > cursor &&
            Number.isSafeInteger(length) &&
            length > space - cursor + 1 &&
            cursor + length <= payload.length,
          "Invalid PAX record",
        );
        const row = payload.toString("utf8", space + 1, cursor + length - 1),
          equals = row.indexOf("=");
        assert.ok(equals > 0, "Invalid PAX record");
        metadata[row.slice(0, equals)] = row.slice(equals + 1);
        cursor += length;
      }
      if (type === 103)
        assert.ok(
          !["path", "linkpath", "size"].some((key) => key in metadata),
          "unsafe entry in archive",
        );
      else pending = metadata;
      continue;
    }
    name = pending.path ?? name;
    assert.ok(
      !pending.linkpath && (!pending.size || Number(pending.size) === size),
      "unsafe entry in archive",
    );
    pending = {};
    assert.ok(
      name &&
        !name.startsWith("/") &&
        !name.split("/").includes("..") &&
        [0, 48, 53].includes(type),
      "unsafe entry in archive",
    );
    total += size;
    assert.ok(total <= limit, "expanded archive exceeds size limit");
    const target = resolve(root, name);
    assert.ok(
      target === root || target.startsWith(root + sep),
      "unsafe entry in archive",
    );
    if (type === 53) directory(target);
    else {
      assert.notEqual(target, root, "unsafe entry in archive");
      directory(dirname(target));
      writeFileSync(target, payload, { flag: "wx", mode: mode & 0o755 });
      chmodSync(target, mode & 0o755);
    }
  }
}
