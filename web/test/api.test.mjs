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
