import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";
import { serverArchiveName } from "./release-assets.mjs";
import { extractSmokeArchive } from "./smoke-archive.mjs";
import { createScratch, removeScratch } from "./test-scratch.mjs";

export async function smokeServer(directory, target, env = process.env) {
  const native = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  assert.equal(target, native, "server smoke must run on the target OS and architecture");
  const version = releaseVersion(env.RELEASE_TAG);
  const artifacts = resolve("build/artifacts/server-smoke");
  await mkdir(artifacts, { recursive: true });
  await rm(join(artifacts, "report.json"), { force: true });
  const run = await createScratch("server-smoke");
  let child, closed, watchdog, output = "";
  try {
    await Promise.all(["tmp", "app", "home", "project"].map((name) => mkdir(join(run, name))));
    const app = join(run, "app");
    extractSmokeArchive(directory, serverArchiveName(version, target), app);
    const binary = join(app, target.startsWith("windows-") ? "spool-server.exe" : "spool-server");
    const isolated = {
      ...env, DISPLAY: "", WAYLAND_DISPLAY: "", HOME: join(run, "home"), USERPROFILE: join(run, "home"),
      SPOOL_HOME: join(run, "data"), CODEX_HOME: join(run, "home/codex"), CLAUDE_CONFIG_DIR: join(run, "home/claude"),
      SPOOL_CODEX_BIN: join(run, "missing-codex"), SPOOL_CLAUDE_BIN: join(run, "missing-claude"),
      SPOOL_RELAY_REGISTRATION_TOKEN: "", TMPDIR: join(run, "tmp"), TMP: join(run, "tmp"), TEMP: join(run, "tmp"),
    };
    const command = (...args) => execFileSync(binary, args, { env: isolated, encoding: "utf8", timeout: 30_000 });
    assert.match(command("--help"), /Spool headless/);
    assert.match(command("help", "project", "member", "set"), /--?own-access/);
    assert.equal(command("version").trim(), version);
    const user = JSON.parse(execFileSync(binary, ["user", "add", "--username", "smoke"], {
      env: { ...isolated, SPOOL_NEW_PASSWORD: "smoke-test-password-only" }, encoding: "utf8", timeout: 30_000,
    }));
    const project = JSON.parse(command("project", "add", "--name", "Smoke", "--directory", join(run, "project"), "--members", "smoke"));
    assert.deepEqual(project.members, [user.id]);
    command("agent", "set", "--backend", "codex", "--enabled=false");
    command("agent", "set", "--backend", "claude", "--enabled=false");
    const listener = createServer();
    await new Promise((done, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", done); });
    const port = listener.address().port;
    await new Promise((done) => listener.close(done));
    child = spawn(binary, ["serve", "--host", "127.0.0.1", "--port", String(port)], { env: isolated, stdio: ["ignore", "pipe", "pipe"] });
    closed = new Promise((done, reject) => { child.once("error", reject); child.once("close", (code) => done(code)); });
    watchdog = setTimeout(() => child?.kill("SIGKILL"), 45_000);
    child.stdout.on("data", (data) => { output = (output + data).slice(-65536); });
    child.stderr.on("data", (data) => { output = (output + data).slice(-65536); });
    let ready = false;
    const origin = `http://127.0.0.1:${port}`;
    for (let i = 0; i < 100; i++) {
      if (child.exitCode !== null) throw new Error(`Server exited during startup: ${output}`);
      try { ready = (await fetch(`${origin}/healthz`, { signal: AbortSignal.timeout(500) })).ok; } catch {}
      if (ready) break;
      await new Promise((done) => setTimeout(done, 100));
    }
    assert.ok(ready, `headless server never became ready: ${output}`);
    assert.deepEqual(await (await fetch(`${origin}/api/version`)).json(), { version });
    assert.match(await (await fetch(origin)).text(), /<html/);
    assert.equal((await fetch(`${origin}/api/projects`)).status, 401);
    const login = await fetch(`${origin}/api/login`, {
      method: "POST", headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({ username: "smoke", password: "smoke-test-password-only" }),
    });
    assert.equal(login.status, 200);
    const cookie = login.headers.get("set-cookie").split(";")[0];
    const projects = await (await fetch(`${origin}/api/projects`, { headers: { Cookie: cookie } })).json();
    assert.ok(projects.some((p) => p.id === project.id));
    child.kill("SIGTERM");
    const code = await closed;
    if (process.platform !== "win32") assert.equal(code, 0, output);
    child = undefined;
  } catch (error) {
    output = `${output}\n${error.stack ?? error}\n${error.stderr ?? ""}`.slice(-65536);
    throw error;
  } finally {
    clearTimeout(watchdog);
    if (child) { child.kill("SIGKILL"); await closed.catch(() => {}); }
    try {
      await writeFile(join(artifacts, "startup.log"), output);
    } finally {
      await removeScratch(run);
    }
  }
  await writeFile(join(artifacts, "report.json"), JSON.stringify({ target, version, gui: false, web: true, login: true, project: true }, null, 2));
  console.log(`PASS ${target}: headless CLI, SQLite, embedded Web, login and project access (${version})`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await smokeServer(process.argv[2], process.argv[3]);
}
