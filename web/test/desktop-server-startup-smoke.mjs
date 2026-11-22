// Native startup with saved profiles, using bundled assets and then real Vite.
// Build build/spool-desktop-dev first; run through tools/run.sh.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../../", import.meta.url));
const output = path.join(root, "build/artifacts/desktop-servers");
const cleanup = [];
let child;
try {
  await mkdir(output, { recursive: true });
  const vite = await createTestViteServer(
    { after: (fn) => cleanup.push(fn) },
    {
      configFile: false,
      root: path.join(root, "web"),
      plugins: [react()],
      server: { host: "127.0.0.1", port: 0 },
    },
  );
  await vite.listen();
  const origin = `http://127.0.0.1:${vite.httpServer.address().port}`;
  for (const [mode, devURL] of [["bundled", ""], ["dev", origin]]) {
    const report = path.join(output, `startup-${mode}.json`);
    child = spawn(
      path.join(root, "build/spool-desktop-dev"),
      ["--release-smoke", report, "--smoke-probe", "server-startup"],
      {
        cwd: root,
        env: { ...process.env, MYGO_DEV_URL: devURL, MYGO_ENV: "development" },
        stdio: "inherit",
      },
    );
    const current = child;
    const timeout = setTimeout(() => current.kill("SIGTERM"), 60000);
    const code = await new Promise((resolve, reject) => {
      current.once("error", reject);
      current.once("exit", resolve);
    }).finally(() => clearTimeout(timeout));
    child = undefined;
    assert.equal(code, 0, `${mode} native startup probe`);
    const data = JSON.parse(await readFile(report, "utf8"));
    assert.equal(data.dev_frontend, mode === "dev");
    assert.equal(data.background_signed_in, true);
    assert.equal(data.background_signed_out, true);
    assert.equal(data.first_socket_retry_preserved, true);
    assert.equal(data.startup_failure_recovery, true);
    assert.equal(data.selection_preserved, true);
    assert.equal(data.disabled_requests, 0);
    console.log(`${mode}: enabled profiles connected without tab selection; disabled profile made zero requests; initial outage recovered`);
  }
} finally {
  if (child && child.exitCode === null && child.signalCode === null) {
    const exited = new Promise((resolve) => child.once("exit", resolve));
    child.kill("SIGTERM");
    await exited;
  }
  for (const close of cleanup.reverse()) await close();
}
