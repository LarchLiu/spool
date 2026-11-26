import assert from "node:assert/strict";
import test from "node:test";

test("API errors preserve permission codes and status while retaining sign-in expiry events", async (t) => {
  const previousNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["en"] } });
  globalThis.window = new EventTarget();
  t.after(() => {
    globalThis.fetch = previousFetch;
    globalThis.window = previousWindow;
    if (previousNavigator) Object.defineProperty(globalThis, "navigator", previousNavigator);
    else delete globalThis.navigator;
  });
  const { api, APIError, setAPILanguage } = await import("../src/api.ts");
  setAPILanguage("en");
  let expired = 0;
  window.addEventListener("auth-expired", () => expired++);
  for (const [status, path, body, code, message] of [
    [403, "/chats/chat-12345678/file", { code: "file_read_denied", error: "OS denied access" }, "file_read_denied", "OS denied access"],
    [403, "/chats/chat-12345678/file", { error: "Project denied access" }, "", "Project denied access"],
    [401, "/me", { error: "Please sign in" }, "", "Please sign in"],
    [401, "/login", { error: "Incorrect password" }, "", "Incorrect password"],
    [500, "/me", null, "", "Request failed (500)"],
  ]) {
    globalThis.fetch = async () => Response.json(body, { status });
    await assert.rejects(api(path), (error) => {
      assert.ok(error instanceof APIError);
      assert.equal(error.status, status);
      assert.equal(error.code, code);
      assert.equal(error.message, message);
      return true;
    });
  }
  assert.equal(expired, 1);
});

test("an interrupted successful response rejects instead of returning error-shaped chat data", async (t) => {
  const previousNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["en"] } });
  globalThis.window = new EventTarget();
  t.after(() => {
    globalThis.fetch = previousFetch;
    globalThis.window = previousWindow;
    if (previousNavigator) Object.defineProperty(globalThis, "navigator", previousNavigator);
    else delete globalThis.navigator;
  });
  const { api, APIError, setAPILanguage } = await import("../src/api.ts");
  setAPILanguage("en");
  let expired = 0;
  window.addEventListener("auth-expired", () => expired++);
  for (const body of [
    '{"id":"chat-12345678","turns":[',
    "",
    new ReadableStream({ start(controller) { controller.error(new TypeError("relay connection lost")); } }),
  ]) {
    globalThis.fetch = async () => new Response(body, { status: 200 });
    await assert.rejects(api("/chats/chat-12345678"), error => {
      assert.ok(error instanceof APIError);
      assert.equal(error.message, "The server returned an invalid response");
      assert.equal(error.status, 200);
      return true;
    });
  }
  assert.equal(expired, 0, "a broken relay response must not sign out the account");
  globalThis.fetch = async () => Response.json({ id: "chat-12345678", turns: [], transcript: { parts: [] } });
  assert.equal((await api("/chats/chat-12345678")).id, "chat-12345678", "a later response can recover without reload");
});
