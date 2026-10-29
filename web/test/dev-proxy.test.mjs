import { test } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createServer, loadConfigFromFile } from "vite";

test("desktop dev starts a pinned Vite origin without silently changing ports", async () => {
  const config = JSON.parse(
    await readFile(new URL("../../mygo.json", import.meta.url), "utf8"),
  );
  assert.equal(config.devUrl, "http://127.0.0.1:5173");
  assert.equal(
    config.devCommand,
    "npm --prefix web run dev -- --port 5173 --strictPort",
  );
  const makefile = await readFile(
    new URL("../../Makefile", import.meta.url),
    "utf8",
  );
  assert.match(
    makefile,
    /dev: web\n\tgo run github\.com\/egoist\/mygo\/cmd\/mygo@v0\.6\.4 dev/,
  );
});

test("the Vite API proxy preserves the dev origin and CSRF credentials", async (t) => {
  const backend = http.createServer((request, response) => {
    request.resume();
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify(request.headers));
  });
  await new Promise((resolve) => backend.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => backend.close(resolve)));
  const loaded = await loadConfigFromFile(
    { command: "serve", mode: "development" },
    fileURLToPath(new URL("../vite.config.ts", import.meta.url)),
  );
  const config = loaded.config;
  const apiProxy = config.server.proxy["/api"];
  assert.equal(typeof apiProxy, "object");
  const vite = await createServer({
    ...config,
    configFile: false,
    root: fileURLToPath(new URL("..", import.meta.url)),
    logLevel: "silent",
    server: {
      ...config.server,
      middlewareMode: true,
      hmr: false,
      proxy: {
        "/api": {
          ...apiProxy,
          target: `http://127.0.0.1:${backend.address().port}`,
        },
      },
    },
  });
  t.after(() => vite.close());
  const frontend = http.createServer(vite.middlewares);
  await new Promise((resolve) => frontend.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => frontend.close(resolve)));
  const port = frontend.address().port;
  const url = `http://127.0.0.1:${port}`;
  const post = (headers, body = "") =>
    new Promise((resolve, reject) => {
      const request = http.request(
        url + "/api/preferences",
        { method: "POST", headers },
        (response) => {
          let text = "";
          response.setEncoding("utf8");
          response.on("data", (chunk) => (text += chunk));
          response.on("end", () => resolve(JSON.parse(text)));
        },
      );
      request.on("error", reject);
      request.end(body);
    });
  for (const host of [`127.0.0.1:${port}`, `localhost:${port}`]) {
    const origin = `http://${host}`;
    const forwarded = await post(
      {
        Host: host,
        Origin: origin,
        "Sec-Fetch-Site": "same-origin",
        "Content-Type": "application/json",
        "X-CSRF-Token": "fixture-csrf",
        Cookie: "spool_session=fixture-session",
      },
      JSON.stringify({ theme: "system", language: "zh-CN" }),
    );
    assert.equal(forwarded.host, host);
    assert.equal(forwarded.origin, origin);
    assert.equal(forwarded["sec-fetch-site"], "same-origin");
    assert.equal(forwarded["x-csrf-token"], "fixture-csrf");
    assert.equal(forwarded.cookie, "spool_session=fixture-session");
  }
  const forwarded = await post({
    Origin: "https://foreign.example",
    "Sec-Fetch-Site": "cross-site",
  });
  assert.equal(forwarded.origin, "https://foreign.example");
  assert.equal(forwarded["sec-fetch-site"], "cross-site");
  assert.notEqual(forwarded.origin, "http://" + forwarded.host);
});
