import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createProfileCache,
  profileLookup,
} from "../.test-build/user-profiles.js";
import {
  reconcile,
  reconcileMetadata,
  turnOperator,
} from "../.test-build/chat-state.js";

const person = (seed, nickname = "Bob") => ({
  id: "bob",
  nickname,
  avatar: { style: "thumbs", seed },
});
const snapshot = (revision, seed, nickname) => ({
  revision,
  people: [person(seed, nickname)],
});
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

test("all chat identities resolve current profiles rather than delayed SSE fields", () => {
  const cache = createProfileCache();
  cache.accept(snapshot(2, "new", "Robert"));
  const old = {
    id: "chat",
    user_id: "bob",
    updated_at: 1,
    participants: [person("old")],
    turns: [],
    transcript: { parts: [] },
  };
  const next = reconcileMetadata(reconcile(old, { ...old }), { ...old });
  const resolve = profileLookup(cache.snapshot());
  assert.deepEqual(
    turnOperator(next, { user_id: "bob" }, null, resolve),
    person("new", "Robert"),
  );
  assert.equal(resolve(next.participants[0].id).avatar.seed, "new");
  assert.deepEqual(resolve("spool-bot"), {
    id: "spool-bot",
    nickname: "Spool Bot",
    bot: true,
  });
  assert.deepEqual(resolve("missing"), { id: "missing", nickname: "missing" });
});

test("profile revisions reject out-of-order updates and avatar save acknowledgements", () => {
  const cache = createProfileCache();
  cache.accept(snapshot(3, "new"));
  cache.accept(snapshot(2, "old"));
  cache.acceptSaved(snapshot(2, "old-save"), "bob");
  assert.equal(cache.snapshot().revision, 3);
  assert.equal(profileLookup(cache.snapshot())("bob").avatar.seed, "new");
});

test("an avatar save acknowledgement updates values without rolling directory membership back", () => {
  const cache = createProfileCache();
  const alice = { id: "alice", nickname: "Alice" };
  cache.accept({
    revision: 2,
    people: [alice, { id: "charlie", nickname: "Charlie" }],
  });
  cache.acceptSaved(
    { revision: 2, people: [alice, person("revoked")] },
    "alice",
  );
  assert.deepEqual(
    cache.snapshot().people.map((person) => person.id),
    ["alice", "charlie"],
  );
});

test("one batched HTTP lookup is shared, and a push wins over its delayed response", async () => {
  const cache = createProfileCache();
  const pending = deferred();
  const signal = new AbortController().signal;
  const first = cache.load(() => pending.promise, signal);
  const second = cache.load(() => {
    throw new Error("duplicate request");
  }, signal);
  assert.equal(first, second);
  cache.accept(snapshot(2, "new"));
  pending.resolve(snapshot(1, "old"));
  await first;
  assert.equal(profileLookup(cache.snapshot())("bob").avatar.seed, "new");
});

test("same-revision permission snapshots replace membership and fence older HTTP reads", async () => {
  const cache = createProfileCache();
  cache.accept(snapshot(1, "avatar"));
  const pending = deferred();
  const request = cache.load(
    () => pending.promise,
    new AbortController().signal,
  );
  cache.accept({ revision: 1, people: [] });
  pending.resolve(snapshot(1, "avatar"));
  await request;
  assert.deepEqual(cache.snapshot().people, []);
});

test("a new lookup does not reuse an aborted request", async () => {
  const cache = createProfileCache();
  const pending = deferred();
  const controller = new AbortController();
  const first = cache.load(() => pending.promise, controller.signal);
  controller.abort();
  await cache.load(
    async () => snapshot(2, "current"),
    new AbortController().signal,
  );
  pending.resolve(snapshot(1, "aborted"));
  await first;
  assert.equal(profileLookup(cache.snapshot())("bob").avatar.seed, "current");
});

test("logout and cancellation cannot restore the previous account's identities", async () => {
  const cache = createProfileCache();
  let updates = 0;
  const unsubscribe = cache.subscribe(() => updates++);
  cache.accept(snapshot(1, "old"));
  const pending = deferred();
  const controller = new AbortController();
  const request = cache.load(() => pending.promise, controller.signal);
  cache.reset();
  pending.resolve(snapshot(2, "late"));
  await request;
  assert.equal(cache.snapshot().revision, -1);
  controller.abort();
  await cache.load(async () => snapshot(3, "cancelled"), controller.signal);
  assert.equal(cache.snapshot().revision, -1);
  assert.equal(updates, 2);
  unsubscribe();
});
