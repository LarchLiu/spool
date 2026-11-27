import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ComposerScreenshot } from "../.test-build/components/web/src/composer-screenshot.js";
import {
  screenshotReceiver,
  screenshotSupported,
  screenshotURL,
} from "../.test-build/native-screenshot.js";

test("unsupported clients render without native hooks or secure-context crypto", () => {
  const crypto = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  try {
    Object.defineProperty(globalThis, "crypto", {
      value: undefined,
      configurable: true,
    });
    const html = renderToStaticMarkup(
      createElement(ComposerScreenshot, {
        enabled: true,
        context: "browser",
        onFiles() {},
        onError() {},
        onComplete() {},
        t: (key) => key,
      }),
    );
    assert.equal(html, "");
  } finally {
    if (crypto) Object.defineProperty(globalThis, "crypto", crypto);
    else delete globalThis.crypto;
  }
});

function fixture() {
  const state = { enabled: true, context: "user/project/chat/draft" };
  const batches = [],
    errors = [],
    busy = [];
  const receiver = screenshotReceiver({
    state: () => state,
    onFiles: (files) => batches.push(files),
    onError: (error) => errors.push(error),
    onBusy: (value) => busy.push(value),
  });
  return { state, batches, errors, busy, receiver };
}
const image = {
  name: "screenshot.png",
  type: "image/png",
  modified: 123,
  data: "AP8qDQo=",
};

test("screenshot capability is shared by macOS, Windows and Linux desktop", () => {
  for (const surface of [
    {},
    { mygo: { platform: "darwin" } },
    { spoolDesktop: true, mygo: { platform: "android" } },
    { spoolDesktop: true, mygo: { platform: "web" } },
  ]) {
    assert.equal(screenshotSupported(surface), false);
  }
  for (const platform of ["darwin", "win32", "linux"]) {
    assert.equal(
      screenshotSupported({ spoolDesktop: true, mygo: { platform } }),
      true,
    );
  }
});

test("screenshot actions only control main-window visibility", () => {
  for (const visibility of ["hide", "show"]) {
    const url = new URL(
      screenshotURL(visibility, "request"),
      "http://spool.test",
    );
    assert.equal(url.pathname, "/__desktop/screenshot");
    assert.equal(url.searchParams.get("visibility"), visibility);
    assert.equal(url.searchParams.get("token"), "request");
    assert.equal(url.searchParams.size, 2);
  }
});

test("screenshot delivery preserves bytes once and only for its request", async () => {
  const { receiver, busy, batches } = fixture();
  assert.equal(receiver.start("one"), true);
  assert.equal(receiver.start("two"), false);
  assert.equal(receiver.receive({ token: "two", files: [image] }), false);
  assert.equal(receiver.receive({ token: "one", files: [image] }), true);
  assert.equal(receiver.receive({ token: "one", files: [image] }), false);
  assert.deepEqual(busy, [true, false]);
  const [file] = batches[0];
  assert.equal(file.name, image.name);
  assert.equal(file.type, image.type);
  assert.equal(file.lastModified, 123);
  assert.deepEqual(
    new Uint8Array(await file.arrayBuffer()),
    new Uint8Array([0, 255, 42, 13, 10]),
  );
});

test("cancel, error and invalid data settle the screenshot without attaching files", () => {
  const { receiver, errors, batches } = fixture();
  for (const extra of [
    {},
    { error: "Permission required" },
    { files: [{ ...image, data: "!" }] },
  ]) {
    assert.equal(receiver.start("one"), true);
    assert.equal(receiver.receive({ token: "one", ...extra }), true);
  }
  assert.deepEqual(errors, [
    "Permission required",
    "Could not read the screenshot",
  ]);
  assert.deepEqual(batches, []);
});

test("changed drafts, read-only composers and reset reject late screenshots", () => {
  for (const change of [
    (f) => {
      f.state.context = "another draft";
    },
    (f) => {
      f.state.enabled = false;
    },
    (f) => f.receiver.reset(),
  ]) {
    const f = fixture();
    f.receiver.start("one");
    change(f);
    assert.equal(f.receiver.receive({ token: "one", files: [image] }), false);
    assert.deepEqual(f.batches, []);
    assert.equal(f.busy.at(-1), false);
  }
  const f = fixture();
  f.state.enabled = false;
  assert.equal(f.receiver.start("one"), false);
});
