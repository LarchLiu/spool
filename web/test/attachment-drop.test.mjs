import { test } from "node:test";
import assert from "node:assert/strict";
import {
  attachmentFiles,
  hasAttachmentFiles,
} from "../.test-build/attachment-selection.js";

test("file drags are recognized while the browser protects their contents", () => {
  assert.equal(
    hasAttachmentFiles({ types: ["Files"], files: [], items: [] }),
    true,
  );
  assert.equal(
    hasAttachmentFiles({ types: [], files: [], items: [{ kind: "file" }] }),
    true,
  );
  assert.equal(
    hasAttachmentFiles({
      types: [],
      files: [new File(["notes"], "notes.txt")],
      items: [],
    }),
    true,
  );
});

test("dragged text, HTML and links are not interpreted as attachments", () => {
  assert.equal(hasAttachmentFiles(null), false);
  const transfer = {
    types: ["text/plain", "text/html", "text/uri-list"],
    files: [],
    items: [
      { kind: "string", getAsFile: () => assert.fail("text is not a file") },
    ],
  };
  assert.equal(hasAttachmentFiles(transfer), false);
  assert.deepEqual(attachmentFiles(transfer), []);
});

test("drops preserve original files and order without duplicating the item fallback", () => {
  const files = [
    new File(["image"], "图片.png", { type: "image/png" }),
    new File(["notes"], "notes.txt", { type: "text/plain" }),
  ];
  const transfer = {
    files,
    items: [
      { kind: "file", getAsFile: () => assert.fail("files already provided") },
    ],
  };
  assert.deepEqual(attachmentFiles(transfer), files);
  assert.equal(attachmentFiles(transfer)[0], files[0]);
  assert.deepEqual(
    attachmentFiles({
      files: [],
      items: [
        { kind: "file", getAsFile: () => files[0] },
        { kind: "file", getAsFile: () => null },
        { kind: "file", getAsFile: () => files[1] },
      ],
    }),
    files,
  );
});
