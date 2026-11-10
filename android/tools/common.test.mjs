import assert from "node:assert/strict";
import test from "node:test";
import { command } from "./common.mjs";

test("optional commands expose exit failures while execution errors still abort", () => {
  const args = ["-e", 'console.log("probe output"); process.exit(3)'];
  const result = command(process.execPath, args, { check: false });
  assert.equal(result.status, 3);
  assert.equal(result.stdout.trim(), "probe output");
  assert.throws(() => command(process.execPath, args), /probe output/);
  assert.throws(
    () => command("/spool-command-not-installed", [], { check: false }),
    /ENOENT/,
  );
});
