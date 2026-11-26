import assert from "node:assert/strict";
import test from "node:test";
import { fetchDownload, saveBrowserDownload } from "../src/file-download.ts";

const translate = (key) => key;

function environment(t) {
  const previous = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window };
  globalThis.document = { documentElement: { lang: "zh-CN" } };
  globalThis.window = new EventTarget();
  t.after(() => Object.assign(globalThis, previous));
}

test("save streams files above the upload limit and reports only after the writer closes", async (t) => {
  environment(t);
  let received = 0, closed = false, release;
  const finish = new Promise(resolve => release = resolve);
  const chunks = 70, chunkSize = 1024 * 1024;
  let sent = 0;
  globalThis.fetch = async (url, options) => {
    assert.equal(url, "/api/chats/chat/file?path=large.bin&download=1&workspace=%2Ftree");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.headers["X-UI-Language"], "zh-CN");
    return new Response(new ReadableStream({ pull(controller) {
      if (sent++ === chunks) controller.close();
      else controller.enqueue(new Uint8Array(chunkSize));
    } }));
  };
  const handle = { name: "large (2).bin", createWritable: async () => new WritableStream({
    write(chunk) { received += chunk.length; },
    async close() { await finish; closed = true; },
  }) };
  let completed = false;
  const pending = saveBrowserDownload("/api/chats/chat/file?path=large.bin&download=1&workspace=%2Ftree", "large.bin", translate, new AbortController().signal, Promise.resolve(handle))
    .then(result => { completed = true; return result; });
  while (received < chunks * chunkSize) await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(completed, false);
  release();
  assert.deepEqual(await pending, { state: "saved", path: "large (2).bin" });
  assert.equal(closed, true);
});

test("broken transfers abort the partial file and disk close errors never report success", async (t) => {
  environment(t);
  let aborted = false;
  globalThis.fetch = async () => new Response(new ReadableStream({ start(controller) { controller.error(new Error("connection lost")); } }));
  const handle = { name: "report.pdf", createWritable: async () => new WritableStream({ abort() { aborted = true; } }) };
  await assert.rejects(saveBrowserDownload("/file", "report.pdf", translate, new AbortController().signal, Promise.resolve(handle)), /connection lost/);
  assert.equal(aborted, true);
  globalThis.fetch = async () => new Response("report");
  handle.createWritable = async () => new WritableStream({ close() { throw new Error("disk full"); } });
  await assert.rejects(saveBrowserDownload("/file", "report.pdf", translate, new AbortController().signal, Promise.resolve(handle)), /disk full/);
});

test("HTTP failures preserve the server reason and authentication expiry", async (t) => {
  environment(t);
  let expired = 0;
  window.addEventListener("auth-expired", () => expired++);
  for (const status of [401, 403, 404]) {
    globalThis.fetch = async () => Response.json({ error: "File unavailable" }, { status });
    await assert.rejects(fetchDownload("/file", translate, new AbortController().signal), /File unavailable/);
  }
  assert.equal(expired, 1);
  globalThis.fetch = async () => new Response(null, { status: 502 });
  await assert.rejects(fetchDownload("/file", translate, new AbortController().signal, "HEAD"), /Request failed \(502\)/);
});

test("cancelling the save picker never fetches a file", async (t) => {
  environment(t);
  globalThis.fetch = async () => assert.fail("cancelled picker fetched");
  await assert.rejects(saveBrowserDownload("/file", "report.pdf", translate, new AbortController().signal,
    Promise.reject(new DOMException("cancelled", "AbortError"))), error => error.name === "AbortError");
});

test("browsers without a save API hand large files to the download manager without buffering", async (t) => {
  environment(t);
  const link = { click() { assert.equal(this.href, "/file?download=1"); assert.equal(this.download, "large.bin"); }, remove() {} };
  document.createElement = () => link;
  document.body = { append() {} };
  globalThis.fetch = async (url, options) => {
    assert.equal(options.method, "HEAD");
    return new Response(null, { headers: { "Content-Length": String(5 * 1024 ** 3) } });
  };
  assert.deepEqual(await saveBrowserDownload("/file?download=1", "large.bin", translate, new AbortController().signal), { state: "handed-off", path: "large.bin" });
});
