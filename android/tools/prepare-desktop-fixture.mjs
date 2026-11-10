#!/usr/bin/env bun
// Generate synthetic Annex-B streams for the offline Android desktop probe.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, requireRunTmp, command } from "./common.mjs";
requireRunTmp();
const output = join(ROOT, "build/android/native-verify/assets/desktop");
mkdirSync(output, { recursive: true });
for (let stripe = 0; stripe < 2; stripe++) {
  const source = `testsrc2=size=640x180:rate=5,drawbox=x=${60 + stripe * 260}:y=40:w=180:h=90:color=${stripe === 0 ? "red" : "blue"}:t=fill`;
  const encoded = command(
    "ffmpeg",
    [
      "-v",
      "error",
      "-f",
      "lavfi",
      "-i",
      source,
      "-frames:v",
      "8",
      "-an",
      "-c:v",
      "libx264",
      "-profile:v",
      "high",
      "-pix_fmt",
      "yuv420p",
      "-preset",
      "ultrafast",
      "-tune",
      "zerolatency",
      "-x264-params",
      "keyint=4:min-keyint=4:scenecut=0:aud=1:repeat-headers=1:bframes=0",
      "-f",
      "h264",
      "pipe:1",
    ],
    { text: false },
  ).stdout;
  const starts = [];
  for (
    let at = 0;
    (at = encoded.indexOf(Buffer.from([0, 0, 0, 1, 9]), at)) !== -1;
    at++
  )
    starts.push(at);
  if (starts.length !== 8) throw new Error("Expected 8 H.264 access units");
  starts.push(encoded.length);
  const packets = [];
  for (let frame = 0; frame < 8; frame++) {
    const access = encoded.subarray(starts[frame], starts[frame + 1]);
    let key = false;
    for (
      let at = 0;
      (at = access.indexOf(Buffer.from([0, 0, 1]), at)) !== -1;
      at++
    )
      if ((access[at + 3] & 31) === 5) key = true;
    const header = Buffer.alloc(16);
    header[0] = 1;
    header[1] = Number(key);
    header.writeUInt32BE(frame + 1, 2);
    header[10] = stripe;
    header[11] = 2;
    header[12] = 2;
    header.writeUInt16BE(stripe * 180, 14);
    const packet = Buffer.concat([header, access]),
      length = Buffer.alloc(4);
    length.writeUInt32BE(packet.length);
    packets.push(length, packet);
  }
  writeFileSync(join(output, `stripe-${stripe}.bin`), Buffer.concat(packets));
}
console.log("Prepared 8 synthetic frames in 2 independent H.264 stripes.");
