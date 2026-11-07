#!/usr/bin/env node
// Read-only RPC diagnostics. Never creates a thread, starts a turn, or writes permissions.
import { execFileSync, spawn } from "node:child_process";
import { realpath } from "node:fs/promises";
import { createInterface } from "node:readline";

async function main() {
  const [project, binary = process.env.SPOOL_CODEX_BIN || "codex"] = process.argv.slice(2);
  if (!project || project === "--help" || project === "-h") {
    console.log("Usage: node tools/probe-codex-permissions.mjs <project-directory> [codex-binary]");
    if (!project) process.exitCode = 1;
    return;
  }
  const directory = await realpath(project);
  const version = execFileSync(binary, ["--version"], {
    cwd: directory, encoding: "utf8", timeout: 5_000,
  }).trim();
  console.log("Codex:", version);
  console.log("Executable:", binary);
  console.log("Project:", directory);

  const child = spawn(binary, [
    "app-server", "--listen", "stdio://",
    "--config", "sandbox_workspace_write.network_access=false",
  ], {
    cwd: directory,
    detached: process.platform !== "win32",
    stdio: ["pipe", "pipe", "inherit"],
  });
  const lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
  const replies = lines[Symbol.asyncIterator]();
  let transportError, closed = false, interrupted = false, requestID = 0;
  const completion = new Promise(resolve => child.once("close", () => {
    closed = true;
    resolve();
  }));
  const failTransport = error => { transportError = error; lines.close(); };
  child.on("error", failTransport);
  child.stdin.on("error", failTransport);
  const stop = () => {
    if (closed || !child.pid) return;
    try {
      if (process.platform === "win32") child.kill("SIGKILL");
      else process.kill(-child.pid, "SIGKILL");
    } catch (error) {
      if (error.code !== "ESRCH") throw error;
    }
  };
  const interrupt = () => { interrupted = true; stop(); };
  process.once("SIGINT", interrupt);
  process.once("SIGTERM", interrupt);
  const send = value => child.stdin.write(JSON.stringify(value) + "\n");

  async function rpc(method, params) {
    const id = ++requestID;
    console.log("正在检查:", method);
    let timer;
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${method}: 等待响应超过 10 秒`)), 10_000);
    });
    try {
      send({ id, method, params });
      for (;;) {
        const { value: line, done } = await Promise.race([replies.next(), timeout]);
        if (done) throw transportError || new Error(`${method}: Codex 进程退出，未收到响应`);
        let reply;
        try { reply = JSON.parse(line); } catch { continue; }
        if (reply?.id !== id || reply.method !== undefined) continue;
        if (reply.error) throw new Error(`${method}: ${JSON.stringify(reply.error)}`);
        if (!reply.result || typeof reply.result !== "object" || Array.isArray(reply.result))
          throw new Error(`${method}: 接口没有返回有效 result`);
        console.log("OK:", method);
        return reply.result;
      }
    } finally {
      clearTimeout(timer);
    }
  }

  try {
    await rpc("initialize", {
      clientInfo: { name: "spool", title: "Spool", version: "0.1.0" },
      capabilities: { experimentalApi: true, mcpServerOpenaiFormElicitation: true },
    });
    send({ method: "initialized", params: {} });
    await rpc("config/read", { includeLayers: false, cwd: directory });
    await rpc("configRequirements/read", {});
    await rpc("experimentalFeature/list", { limit: 100 });
    console.log("四项接口探测通过");
  } finally {
    try { stop(); } finally {
      await completion;
      lines.close();
      process.removeListener("SIGINT", interrupt);
      process.removeListener("SIGTERM", interrupt);
      if (interrupted) process.exitCode = 130;
    }
  }
}

main().catch(error => {
  console.error("FAILED:", error.message);
  process.exitCode ||= 1;
});
