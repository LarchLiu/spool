import { test } from "node:test";
import assert from "node:assert/strict";
import { imageSource, imageResource, imageFailureMessage } from "../.test-build/components/web/src/image-resource.js";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AttachmentCards } from "../.test-build/components/web/src/attachments.js";

test("image sources share authorized file, upload and saved-tool identities", () => {
  for (const source of [
    "data:image/jpeg;base64,AA==", "blob:owned-draft",
    "/api/chats/chat/attachments?id=upload",
    "/api/chats/chat/tool-image?ref=native-tool-a&index=0",
    "/api/chats/chat/file?image=1&path=%2Fremoved.png",
    "/api/chats/chat/file?path=%2Fremoved.png&download=1&workspace=%2Fworktree",
    "/api/projects/project/file?path=picture.png&image=1",
  ]) {
    assert.equal(imageSource(source), source);
    assert.equal(imageResource("picture", source).url, source);
  }
  assert.equal(imageSource("/api/chats/other/attachments?id=id", "chat"), undefined);
  for (const source of ["https://example.com/track.png", "//example.com/track.png", "file:///tmp/image.png", "data:image/svg+xml;base64,AA==", "/api/avatars.svg", "/api/chats/chat/file?image=1&path=a&path=b", "/api/chats/chat/file?image=1&path=a&redirect=https://example.com", "/api/chats/chat/tool-image?ref=x&index=-1"]) {
    assert.equal(imageSource(source), undefined, source);
  }
  const html = renderToStaticMarkup(createElement(AttachmentCards, { files: [{ id: "bad", name: "track.png", media_type: "image/png", preview_url: "https://example.com/track.png" }], t: key => key, onPreview() {} }));
  assert.doesNotMatch(html, /<img|https:\/\/example.com/);
});

test("image resources retain server-provided download and relay scope", () => {
  const source = "/api/chats/chat/file?image=1&path=picture.png&workspace=%2Fworktree";
  const download = "/api/chats/chat/file?download=1&path=picture.png&workspace=%2Fworktree";
  assert.equal(imageResource("picture", source, download).downloadURL, download);
  const previous = globalThis.location;
  const base = "/web/" + "a".repeat(52) + "/";
  try {
    globalThis.location = { pathname: base };
    assert.equal(imageSource(source), base + source.slice(1));
    assert.equal(imageResource("picture", base + source.slice(1)).url, base + source.slice(1));
  } finally { if (previous === undefined) delete globalThis.location; else globalThis.location = previous; }
});

test("missing, denied, unsupported and transport failures have distinct messages", () => {
  for (const status of [404, 410]) assert.equal(imageFailureMessage(status), "File not found");
  for (const status of [401, 403]) assert.equal(imageFailureMessage(status), "Image access denied");
  assert.equal(imageFailureMessage(400), "This file cannot be previewed as an image");
  for (const status of [0, 500, 503]) assert.equal(imageFailureMessage(status), "Image could not be loaded. Try again.");
});
