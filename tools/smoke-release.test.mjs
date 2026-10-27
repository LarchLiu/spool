import assert from "node:assert/strict";
import test from "node:test";
import { smokeRelease, verifySmokeReport } from "./smoke-release.mjs";

test("release smoke accepts only the expected version, target, native loop and Web probes", () => {
  const valid = { version: "1.2.3", target: "windows-arm64", native: true, web: true };
  assert.doesNotThrow(() => verifySmokeReport(valid, valid.version, valid.target));
  for (const field of Object.keys(valid)) {
    const wrong = { ...valid, [field]: typeof valid[field] === "boolean" ? false : "different" };
    assert.throws(() => verifySmokeReport(wrong, valid.version, valid.target));
  }
  assert.throws(() => verifySmokeReport({}, valid.version, valid.target));
});

test("cross-compiled or invalid-version smoke attempts fail before extracting or running anything", async () => {
  await assert.rejects(smokeRelease("missing", "not-native"), /target OS and architecture/);
  const target = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  await assert.rejects(smokeRelease("missing", target, { RELEASE_TAG: "bad" }), /Release tag/);
});
