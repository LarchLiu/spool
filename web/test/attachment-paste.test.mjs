import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import {
  pasteAttachments,
  prepareAttachments,
  releaseAttachments,
  validateAttachments,
} from "../.test-build/attachment-selection.js";

function paste(clipboardData) {
  const batches = [];
  let prevented = false;
  pasteAttachments(
    { clipboardData, preventDefault: () => (prevented = true) },
    (files) => batches.push(files),
  );
  return { batches, prevented };
}

test("pasting images and files attaches the original clipboard files in order", () => {
  const image = new File(["image bytes"], "screenshot.png", {
    type: "image/png",
  });
  const text = new File(["Notes"], "notes.txt", { type: "text/plain" });
  const result = paste({ files: [image, text], items: [] });
  assert.equal(result.prevented, true);
  assert.deepEqual(result.batches, [[image, text]]);
  assert.equal(result.batches[0][0], image);
  assert.equal(result.batches[0][1], text);
});

test("clipboard items provide the image/file fallback without treating text as attachments", () => {
  const image = new File(["image bytes"], "image.png", { type: "image/png" });
  const file = new File(["%PDF-1.7"], "document.pdf", {
    type: "application/pdf",
  });
  const result = paste({
    files: [],
    items: [
      {
        kind: "string",
        type: "text/plain",
        getAsFile: () => assert.fail("text is not a file"),
      },
      { kind: "file", type: "image/png", getAsFile: () => image },
      { kind: "file", type: "application/octet-stream", getAsFile: () => null },
      { kind: "file", type: "application/pdf", getAsFile: () => file },
    ],
  });
  assert.equal(result.prevented, true);
  assert.deepEqual(result.batches, [[image, file]]);
});

test("the same clipboard file exposed in both lists is not attached twice", () => {
  const image = new File(["image bytes"], "image.png", { type: "image/png" });
  const result = paste({
    files: [image],
    items: [
      {
        kind: "file",
        getAsFile: () => assert.fail("files already contain the image"),
      },
    ],
  });
  assert.deepEqual(result.batches, [[image]]);
});

test("plain text, HTML, paths and URLs retain normal paste without fetching anything", () => {
  for (const clipboard of [
    null,
    { files: [], items: [] },
    {
      files: [],
      items: ["text/plain", "text/html", "text/uri-list"].map((type) => ({
        kind: "string",
        type,
        getAsFile: () => assert.fail("must not read text as a file"),
      })),
      getData: () => assert.fail("must not interpret pasted paths or HTML"),
    },
    { files: [], items: [{ kind: "file", getAsFile: () => null }] },
  ]) {
    assert.deepEqual(paste(clipboard), { batches: [], prevented: false });
  }
});

test("pasted files use existing draft previews, text excerpts and attachment limits", async () => {
  const image = new File(["image bytes"], "screenshot.png", {
    type: "image/png",
  });
  const text = new File(["# Pasted notes\n\nKeep this content."], "notes.md", {
    type: "text/markdown",
  });
  const [selected] = paste({ files: [image, text], items: [] }).batches;
  assert.equal(validateAttachments(selected), null);
  const prepared = await prepareAttachments(selected);
  try {
    assert.equal(prepared[0].file, image);
    assert.equal(prepared[0].name, "screenshot.png");
    assert.match(prepared[0].preview_url, /^blob:/);
    assert.equal(prepared[1].file, text);
    assert.equal(prepared[1].excerpt, "# Pasted notes\n\nKeep this content.");
    assert.equal(prepared[1].preview_url, undefined);
    assert.notEqual(prepared[0].id, prepared[1].id);
  } finally {
    releaseAttachments(prepared);
  }
  assert.equal(
    validateAttachments([
      ...Array.from({ length: 5 }, () => ({ size: 1 })),
      ...selected,
    ]),
    "Attach at most 6 files, up to 16 MB total",
  );
  assert.equal(
    validateAttachments([new File([], "empty.txt")]),
    "Each attachment must be nonempty and at most 4 MB",
  );
  assert.equal(
    validateAttachments([{ size: 4 * 1024 * 1024 + 1 }]),
    "Each attachment must be nonempty and at most 4 MB",
  );
});

test("the message input routes paste through the same guarded addFiles flow as the picker", async () => {
  const source = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    source,
    /onPaste=\{\(event\) => pasteAttachments\(event, addFiles\)\}/,
  );
  assert.match(
    source,
    /if \(!selected.length \|\| preparingFiles \|\| posting \|\| !composerWritable\)/,
  );
  assert.match(
    source,
    /validateAttachments\(\[\.\.\.filesRef.current, \.\.\.selected\]\)/,
  );
  assert.match(source, /setFiles\(\(old\) => \[\.\.\.old, \.\.\.prepared\]\)/);
  assert.match(source, /releaseAttachments\(prepared\)/);
});
