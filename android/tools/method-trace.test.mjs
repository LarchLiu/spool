import assert from "node:assert/strict";
import test from "node:test";
import { summarize } from "./summarize-method-trace.mjs";
test("dual clocks distinguish main-thread CPU from wall time and exclude workers", () => {
  const header = Buffer.from(
    "*version\n3\nclock=dual\ndata-file-overflow=false\n*threads\n1\tmain\n2\tworker\n*methods\n0x4\tcom.cloudgeek.spool.android.Sample\trender\n0x8\tandroid.graphics.Paint\tmeasure\n*end\n",
  );
  const rows = [
      [1, 4, 0, 0],
      [2, 4, 0, 0],
      [1, 8, 100, 1000],
      [1, 9, 300, 3000],
      [1, 5, 400, 5000],
      [2, 5, 9000, 10000],
    ],
    binary = Buffer.alloc(18 + rows.length * 14);
  binary.write("SLOW");
  binary.writeUInt16LE(3, 4);
  binary.writeUInt16LE(18, 6);
  binary.writeUInt16LE(14, 16);
  rows.forEach(([tid, method, cpu, wall], index) => {
    const at = 18 + index * 14;
    binary.writeUInt16LE(tid, at);
    binary.writeUInt32LE(method, at + 2);
    binary.writeUInt32LE(cpu, at + 6);
    binary.writeUInt32LE(wall, at + 10);
  });
  const result = summarize(Buffer.concat([header, binary]));
  assert.equal(result.overflow, false);
  assert.equal(result.sampledMainWallMs, 5);
  assert.equal(result.sampledMainCpuMs, 0.4);
  assert.equal(result.leafWallMs["android.graphics.Paint.measure"], 2);
  assert.equal(result.leafCpuMs["android.graphics.Paint.measure"], 0.2);
  assert.equal(
    result.appInclusiveCpuMs["com.cloudgeek.spool.android.Sample.render"],
    0.4,
  );
  assert.deepEqual(
    result.appMethodChildrenCpuMs["com.cloudgeek.spool.android.Sample.render"],
    { "[self]": 0.2, "android.graphics.Paint.measure": 0.2 },
  );
});
