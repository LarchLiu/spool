import { test } from "node:test";
import assert from "node:assert/strict";
import { nativeAttachmentDrop } from "../.test-build/native-attachment-drop.js";

function fixture() {
  const state = { enabled: true, context: "user/project/chat/draft" };
  let url = "http://localhost/?chat=one";
  const batches = [],
    errors = [],
    hovers = [];
  const receiver = nativeAttachmentDrop({
    state: () => state,
    contains: (x, y) => x >= 20 && x < 200 && y >= 300 && y < 400,
    url: () => url,
    onFiles: (files) => batches.push(files),
    onError: (error) => errors.push(error),
    onHover: (active) => hovers.push(active),
  });
  const delivery = (token) => ({
    token,
    url,
    files: [
      {
        name: "原图.png",
        type: "image/png",
        modified: 1700000000123,
        data: Buffer.from([0, 255, 42, 13, 10]).toString("base64"),
      },
    ],
  });
  return {
    receiver,
    state,
    batches,
    errors,
    hovers,
    delivery,
    navigate: (next) => {
      url = next;
    },
  };
}

test("native hover advertises the composer anywhere in the page and clears on exit or reset", () => {
  const { receiver, state, hovers } = fixture();
  receiver.hover({ active: true, x: 50, y: 50 });
  assert.equal(
    hovers.at(-1),
    true,
    "History hover must advertise the drop location",
  );
  assert.equal(
    receiver.claim(50, 50),
    "",
    "The actual drop still belongs in the composer",
  );
  assert.equal(hovers.at(-1), false);
  receiver.hover({ active: true, x: 50, y: 350 });
  receiver.hover({ active: false, x: -1, y: -1 });
  assert.equal(hovers.at(-1), false);
  receiver.hover({ active: true, x: 50, y: 50 });
  receiver.reset();
  assert.equal(hovers.at(-1), false);
  state.enabled = false;
  receiver.hover({ active: true, x: 50, y: 50 });
  assert.equal(
    hovers.at(-1),
    false,
    "Read-only or busy composers have no drop hint",
  );
});

test("native files preserve bytes and metadata and are delivered only once", async () => {
  const { receiver, batches, delivery } = fixture();
  const token = receiver.claim(50, 350);
  assert.ok(token);
  assert.equal(receiver.claim(50, 350), "", "Only one pending native batch");
  assert.equal(receiver.receive(delivery("another-token")), false);
  assert.equal(receiver.receive(delivery(token)), true);
  assert.equal(receiver.receive(delivery(token)), false);
  assert.equal(batches.length, 1);
  const [file] = batches[0];
  assert.equal(file.name, "原图.png");
  assert.equal(file.type, "image/png");
  assert.equal(file.lastModified, 1700000000123);
  assert.deepEqual(
    new Uint8Array(await file.arrayBuffer()),
    new Uint8Array([0, 255, 42, 13, 10]),
  );
});

test("native drops require an enabled composer at the drop coordinates", () => {
  const { receiver, state, batches } = fixture();
  for (const [x, y] of [
    [0, 0],
    [50, 250],
    [500, 350],
  ])
    assert.equal(receiver.claim(x, y), "");
  state.enabled = false;
  assert.equal(receiver.claim(50, 350), "");
  assert.deepEqual(batches, []);
});

test("switching account, chat, draft, URL or writable state discards in-flight files", () => {
  for (const change of [
    (f) => {
      f.state.context = "other-context";
    },
    (f) => {
      f.state.enabled = false;
    },
    (f) => f.navigate("http://localhost/?chat=two"),
    (f) => f.receiver.reset(),
  ]) {
    const f = fixture();
    const token = f.receiver.claim(50, 350);
    const offered = f.delivery(token);
    change(f);
    assert.equal(f.receiver.receive(offered), false);
    assert.deepEqual(f.batches, []);
  }
  const f = fixture();
  const offered = f.delivery(f.receiver.claim(50, 350));
  offered.url = "http://other-server/";
  assert.equal(f.receiver.receive(offered), false);
});

test("DOM file delivery suppresses its matching native callback, even after a draft reset", () => {
  const { receiver } = fixture();
  receiver.record({
    clientX: 50.2,
    clientY: 350.4,
    dataTransfer: { files: [new File(["notes"], "notes.txt")], items: [] },
  });
  receiver.reset();
  assert.equal(receiver.claim(50, 350), "");
  assert.ok(
    receiver.claim(50, 350),
    "A subsequent drop at the same position still works",
  );
});

test("text-only DOM drops do not block a later native file drop", () => {
  const { receiver } = fixture();
  receiver.record({
    clientX: 50,
    clientY: 350,
    dataTransfer: { files: [], items: [{ kind: "string" }] },
  });
  assert.ok(receiver.claim(50, 350));
});

test("native read failures report an error and allow retry", () => {
  const { receiver, errors, batches, delivery } = fixture();
  const token = receiver.claim(50, 350);
  assert.equal(
    receiver.receive({
      ...delivery(token),
      files: [],
      error: "Could not read the dropped files",
    }),
    true,
  );
  assert.deepEqual(errors, ["Could not read the dropped files"]);
  assert.deepEqual(batches, []);
  assert.ok(receiver.claim(50, 350));
});

test("expired native delivery cannot block later drops or deliver stale files", () => {
  const original = Date.now;
  let now = original();
  Date.now = () => now;
  try {
    const { receiver, batches, delivery } = fixture();
    const token = receiver.claim(50, 350);
    now += 15001;
    assert.equal(receiver.receive(delivery(token)), false);
    const abandoned = receiver.claim(50, 350);
    assert.ok(abandoned);
    now += 15001;
    const next = receiver.claim(50, 350);
    assert.ok(next);
    assert.notEqual(abandoned, next);
    assert.equal(receiver.receive(delivery(abandoned)), false);
    assert.deepEqual(batches, []);
  } finally {
    Date.now = original;
  }
});
