#!/usr/bin/env bun
// Synthetic container/alpha/disposal fixtures; Bun plus the WebP cwebp encoder.
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  writeFileSync,
  rmSync,
} from "node:fs";
import { join } from "node:path";
import { deflateSync } from "node:zlib";
import { crc32 } from "../../tools/archive.mjs";
import { ROOT, cli, command, writeJson, requireRunTmp } from "./common.mjs";
const { values } = cli({
  output: {
    type: "string",
    default: join(ROOT, "android/app/src/test/resources/image-animation"),
  },
});
const output = values.output;
mkdirSync(output, { recursive: true });
const manifest = [];
const u16 = (value) => {
  const b = Buffer.alloc(2);
  b.writeUInt16LE(value);
  return b;
};
const u24 = (value) => {
  const b = Buffer.alloc(3);
  b.writeUIntLE(value, 0, 3);
  return b;
};
const u32 = (value) => {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(value);
  return b;
};
const save = (name, data, description) => {
  writeFileSync(join(output, name), data);
  manifest.push({ name, description });
};
function tile(width, height, fill, box, color) {
  const bytes = Buffer.alloc(width * height * 4);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++)
      bytes.set(
        box && x >= box[0] && x <= box[2] && y >= box[1] && y <= box[3]
          ? color
          : fill,
        (y * width + x) * 4,
      );
  return { width, height, bytes };
}
const colors = [
    [0, 0, 0, 0],
    [255, 0, 0, 255],
    [0, 255, 0, 255],
    [0, 0, 255, 255],
    [255, 255, 0, 255],
  ],
  palette = Buffer.alloc(24);
colors.forEach((color, i) => palette.set(color.slice(0, 3), i * 3));
const frames = [
  [[0, 0, 3, 3], colors[1]],
  [[2, 2, 5, 5], colors[2]],
  [[4, 0, 7, 3], colors[3]],
  [[0, 4, 7, 5], colors[4]],
].map(([box, color]) => tile(8, 6, colors[0], box, color));
function gif(name, disposals, loop, durations) {
  const parts = [
    Buffer.from("GIF89a"),
    u16(8),
    u16(6),
    Buffer.from([0xf2, 0, 0]),
    palette,
  ];
  if (loop != null)
    parts.push(
      Buffer.from([0x21, 0xff, 11]),
      Buffer.from("NETSCAPE2.0"),
      Buffer.from([3, 1]),
      u16(loop),
      Buffer.from([0]),
    );
  for (let i = 0; i < disposals.length; i++) {
    const frame = frames[i],
      indices = [];
    // Full-sized frame 0 plus cropped later frames exercise local and global palettes.
    const xs = [],
      ys = [];
    for (let y = 0; y < 6; y++)
      for (let x = 0; x < 8; x++) {
        const at = (y * 8 + x) * 4;
        if (frame.bytes[at + 3] || (i && frames[i - 1].bytes[at + 3])) {
          xs.push(x);
          ys.push(y);
        }
      }
    const cropped = i > 0 && disposals[i - 1] !== 2;
    const left = cropped ? Math.min(...xs) : 0,
      top = cropped ? Math.min(...ys) : 0,
      width = cropped ? Math.max(...xs) - left + 1 : 8,
      height = cropped ? Math.max(...ys) - top + 1 : 6;
    for (let y = top; y < top + height; y++)
      for (let x = left; x < left + width; x++) {
        const at = (y * 8 + x) * 4;
        indices.push(frame.bytes[at + 3] ? i + 1 : 0);
      }
    // A clear before each pixel keeps the LZW code width fixed and deterministic.
    const codes = indices.flatMap((value) => [8, value]);
    codes.push(9);
    const packed = Buffer.alloc(Math.ceil(codes.length / 2));
    codes.forEach(
      (code, index) => (packed[index >> 1] |= code << ((index & 1) * 4)),
    );
    parts.push(
      Buffer.from([0x21, 0xf9, 4, disposals[i] * 4 + 1]),
      u16(durations[i] / 10),
      Buffer.from([0, 0, 0x2c]),
      u16(left),
      u16(top),
      u16(width),
      u16(height),
      Buffer.from([i ? 0x82 : 0]),
    );
    if (i) parts.push(palette);
    parts.push(Buffer.from([3, packed.length]), packed, Buffer.from([0]));
  }
  parts.push(Buffer.from([0x3b]));
  save(
    name,
    Buffer.concat(parts),
    name === "fast.gif"
      ? "GIF browser minimum-delay clamping and infinite loop"
      : "GIF local/global palettes, cropped frames, transparency and disposal [" +
          disposals.join(", ") +
          "]",
  );
}
gif("restore.gif", [1, 3, 2, 1], 2, [80, 120, 160, 200]);
gif("clear.gif", [2, 2, 2, 2], undefined, [80, 120, 160, 200]);
gif("fast.gif", [2, 2, 2], 0, [0, 10, 20]);
const chunk = (type, data) =>
  Buffer.concat([
    Buffer.from(type),
    u32(data.length),
    data,
    Buffer.alloc(data.length % 2),
  ]);
function encode(image, lossless) {
  // Encode a minimal RGBA PNG, retaining the RGB bytes of transparent pixels.
  const pngChunk = (type, data) => {
    const name = Buffer.from(type),
      size = Buffer.alloc(4),
      crc = Buffer.alloc(4);
    size.writeUInt32BE(data.length);
    crc.writeUInt32BE(crc32(Buffer.concat([name, data])));
    return Buffer.concat([size, name, data, crc]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(image.width);
  header.writeUInt32BE(image.height, 4);
  header[8] = 8;
  header[9] = 6;
  const rows = [];
  for (let y = 0; y < image.height; y++)
    rows.push(
      Buffer.from([0]),
      image.bytes.subarray(y * image.width * 4, (y + 1) * image.width * 4),
    );
  const png = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(Buffer.concat(rows))),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
  const scratch = mkdtempSync(join(requireRunTmp(), "webp-encode."));
  try {
    const file = join(scratch, "frame.png");
    writeFileSync(file, png);
    return command(
      "cwebp",
      [
        "-quiet",
        ...(lossless ? ["-lossless"] : []),
        "-q",
        "100",
        "-m",
        "6",
        "-exact",
        file,
        "-o",
        "-",
      ],
      { text: false },
    ).stdout;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}
function webp(name, lossless, loops, durations, pattern = false) {
  const data = [
    [0, 0, tile(8, 6, colors[1]), 80, 0],
    [2, 2, tile(4, 4, colors[0], [1, 0, 3, 3], [0, 255, 0, 128]), 120, 1],
    [4, 0, tile(4, 4, colors[3]), 160, 2],
    [0, 2, tile(8, 4, colors[0], [0, 2, 7, 3], colors[4]), 200, 2],
  ];
  if (pattern) data[0][2] = tile(8, 6, colors[1], [4, 0, 7, 5], colors[3]);
  const chunks = [
    chunk(
      "VP8X",
      Buffer.concat([Buffer.from([0x12, 0, 0, 0]), u24(7), u24(5)]),
    ),
    chunk(
      "ANIM",
      Buffer.concat([Buffer.from([0x10, 0x20, 0x30, 0xff]), u16(loops)]),
    ),
  ];
  for (let index = 0; index < data.length; index++) {
    const [x, y, image, duration, flags] = data[index],
      source = encode(image, lossless),
      pixels = [];
    for (let at = 12; at < source.length;) {
      const size = source.readUInt32LE(at + 4),
        end = at + 8 + size + (size % 2);
      if (
        ["ALPH", "VP8 ", "VP8L"].includes(source.toString("ascii", at, at + 4))
      )
        pixels.push(source.subarray(at, end));
      at = end;
    }
    chunks.push(
      chunk(
        "ANMF",
        Buffer.concat([
          u24(x / 2),
          u24(y / 2),
          u24(image.width - 1),
          u24(image.height - 1),
          u24(durations?.[index] ?? duration),
          Buffer.from([flags]),
          ...pixels,
        ]),
      ),
    );
  }
  const body = Buffer.concat([Buffer.from("WEBP"), ...chunks]);
  save(
    name,
    Buffer.concat([Buffer.from("RIFF"), u32(body.length), body]),
    lossless
      ? "WebP VP8L"
      : "WebP VP8 + ALPH; cropped blend, no-blend and clear disposal",
  );
}
webp("blend.webp", true, 2);
webp("alpha-lossy.webp", false, 0);
webp("fast.webp", true, 0, [0, 10, 20, 100]);
webp("oriented-profile.webp", true, 2, undefined, true);
// Fixed synthetic sRGB profile; no native color-management/Pillow dependency.
const profile = Buffer.from(
  "AAACTGxjbXMEQAAAbW50clJHQiBYWVogB+oACgAfAAgAGAASYWNzcEFQUEwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPbWAAEAAAAA0y1sY21zAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALZGVzYwAAAQgAAAA2Y3BydAAAAUAAAABMd3RwdAAAAYwAAAAUY2hhZAAAAaAAAAAsclhZWgAAAcwAAAAUYlhZWgAAAeAAAAAUZ1hZWgAAAfQAAAAUclRSQwAAAggAAAAgZ1RSQwAAAggAAAAgYlRSQwAAAggAAAAgY2hybQAAAigAAAAkbWx1YwAAAAAAAAABAAAADGVuVVMAAAAaAAAAHABzAFIARwBCACAAYgB1AGkAbAB0AC0AaQBuAABtbHVjAAAAAAAAAAEAAAAMZW5VUwAAADAAAAAcAE4AbwAgAGMAbwBwAHkAcgBpAGcAaAB0ACwAIAB1AHMAZQAgAGYAcgBlAGUAbAB5WFlaIAAAAAAAAPbWAAEAAAAA0y1zZjMyAAAAAAABDEIAAAXe///zJQAAB5MAAP2Q///7of///aIAAAPcAADAblhZWiAAAAAAAABvoAAAOPUAAAOQWFlaIAAAAAAAACSfAAAPhAAAtsNYWVogAAAAAAAAYpcAALeHAAAY2XBhcmEAAAAAAAMAAAACZmYAAPKnAAANWQAAE9AAAApbY2hybQAAAAAAAwAAAACj1wAAVHsAAEzNAACZmgAAJmYAAA9c",
  "base64",
);
const base = readFileSync(join(output, "oriented-profile.webp")),
  oriented = Buffer.from(base.subarray(12, 30));
oriented[8] |= 0x28;
const exif = Buffer.from(
  "4578696600004d4d002a00000008000101120003000000010006000000000000",
  "hex",
);
const body = Buffer.concat([
  Buffer.from("WEBP"),
  oriented,
  chunk("ICCP", profile),
  base.subarray(30),
  chunk("EXIF", exif),
]);
writeFileSync(
  join(output, "oriented-profile.webp"),
  Buffer.concat([Buffer.from("RIFF"), u32(body.length), body]),
);
manifest.at(-1).description =
  "Animated WebP with ICC profile and EXIF clockwise orientation";
writeJson(join(output, "manifest.json"), manifest);
console.log(
  `Generated ${manifest.length} bounded native/browser animation fixtures.`,
);
