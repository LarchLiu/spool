import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { turnExchanges, exchangeFiles, turnPresentation } from "../.test-build/components/web/src/turn-presentation.js";

test("Web and Android split steering inputs at the same chronological boundaries", () => {
  const fixtures = JSON.parse(readFileSync(new URL("../../android/app/src/test/resources/turn-exchanges.json", import.meta.url), "utf8"));
  for (const fixture of fixtures) {
    const before = JSON.stringify(fixture.parts);
    const ranges = turnExchanges(fixture.parts);
    assert.deepEqual(ranges.map(range => [range.start, range.inputEnd, range.end]), fixture.ranges, fixture.id);
    assert.deepEqual(ranges.flatMap(range => fixture.parts.slice(range.start, range.end)), fixture.parts, fixture.id + " preserves every part");
    assert.equal(JSON.stringify(fixture.parts), before);
  }
});

test("only the last input's answer stays outside the shared process while attachments stay with each input", () => {
  const parts = [
    { role: "user", kind: "text", text: "First" },
    { role: "user", kind: "image", name: "first.png", image_url: "data:image/png;base64,AA==", text: "[image]" },
    { role: "assistant", kind: "text", phase: "final_answer", text: "First answer" },
    { role: "user", kind: "text", text: "Follow up" },
    { role: "user", kind: "context", name: "notes.txt", file_path: "/work/notes.txt", text: "notes.txt" },
    { role: "assistant", kind: "text", phase: "final_answer", text: "Second answer" },
  ];
  const ranges = turnExchanges(parts);
  const presentation = turnPresentation(parts.slice(ranges[0].inputEnd), false);
  assert.equal(presentation.answer, "Second answer");
  assert.deepEqual(presentation.process, parts.slice(2, 5));
  assert.equal(presentation.expanded, false);
  const files = ranges.map(range => exchangeFiles(parts.slice(range.start, range.inputEnd), "turn", "chat", range.start));
  assert.deepEqual(files.map(items => items.map(item => [item.id, item.name])), [[["turn-image-1", "first.png"]], [["turn-file-4", "notes.txt"]]]);
  assert.equal(files[1][0].preview_url, "/api/chats/chat/file?path=%2Fwork%2Fnotes.txt&download=1");
});

test("a new steering input returns earlier answers to the process and resumes working", () => {
  const prior = { role: "assistant", kind: "text", phase: "final_answer", text: "Prior answer" };
  const input = { role: "user", kind: "text", text: "Please continue" };
  for (const running of [false, true]) {
    const presentation = turnPresentation([prior, input], running);
    assert.equal(presentation.answer, "");
    assert.equal(presentation.expanded, running);
    assert.deepEqual(presentation.process, [prior, input]);
  }
  const unphased = { role: "assistant", kind: "text", text: "Legacy answer" };
  assert.equal(turnPresentation([unphased, input], false).answer, "");
  const tool = { role: "assistant", kind: "tool_use", name: "exec_command", text: "{}" };
  assert.equal(turnPresentation([tool, unphased, input], false).answer, "");
  const last = { role: "assistant", kind: "text", text: "Final legacy answer" };
  const presentation = turnPresentation([unphased, input, last], false);
  assert.equal(presentation.answer, last.text);
  assert.deepEqual(presentation.process, [unphased, input]);
});

test("appending a new user input keeps previous exchange boundaries and source references stable", () => {
  const parts = [{ role: "user", kind: "text", text: "First" }, { role: "assistant", kind: "text", text: "Checking" }];
  const first = turnExchanges(parts)[0];
  const updated = [...parts, { role: "user", kind: "text", text: "Follow up" }];
  assert.deepEqual(turnExchanges(updated), [first, { start: 2, inputEnd: 3, end: 3 }]);
  assert.equal(updated[1], parts[1]);
});
