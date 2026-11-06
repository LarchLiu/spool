import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  ActivityDetail,
  activitiesFromParts,
  changeStats,
  displayActivity,
  ToolActivityList,
  TurnResponse,
  ToolDiffBoundary,
  toolResultDiffs,
  viewedImagePaths,
} from "../.test-build/components/web/src/tool-activity.js";
import { LocalFileContext } from "../.test-build/components/web/src/local-file-context.js";
import { projectFileURL } from "../.test-build/components/web/src/local-file-links.js";
import { turnPresentation, turnWorkLabel } from "../.test-build/components/web/src/turn-presentation.js";
import { shellOperations, toolCommand } from "../.test-build/components/web/src/tool-kind.js";

const t = (message, values = {}) =>
  message.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? key));
const tool = (name, text) => ({ role: "assistant", kind: "tool_use", name, text });
const message = (text, phase, id) => ({ role: "assistant", kind: "text", text, phase, id });

test("compound Codex and Claude commands fold into one summary and have no per-step details", () => {
  const cases = JSON.parse(readFileSync(new URL("../../android/app/src/test/resources/tool-operations.json", import.meta.url), "utf8"));
  for (const fixture of cases) {
    const part = { ...tool(fixture.name, fixture.input ? JSON.stringify(fixture.input) : fixture.text), id: "call" };
    const result = { role: "tool", kind: "tool_result", id: "call", text: "combined output" };
    const original = JSON.stringify(part);
    const activities = activitiesFromParts([part, result], "claude", t);
    if (fixture.operations.length) {
      assert.deepEqual(shellOperations(toolCommand(part.text, fixture.input)), fixture.operations, fixture.id);
      assert.equal(activities.length, fixture.operations.length, fixture.id);
      const projected = { ...part, text: "", tool: { kind: "shell", operations: fixture.operations, href: "/chats/chat/tool-detail?ref=call" } };
      const lazy = activitiesFromParts([projected, result], "claude", t);
      assert.deepEqual(lazy.map(a => [a.kind, a.label, a.noDetails]), activities.map(a => [a.kind, a.label, a.noDetails]), fixture.id + " lazy");
      for (const activity of activities) {
        assert.equal(activity.use, part);
        assert.equal(activity.result, result);
        assert.equal(activity.noDetails, true);
        assert.equal(renderToStaticMarkup(createElement(ActivityDetail, { activity, t })), "");
      }
      for (const input of [[part, result], [projected, result]]) {
        const html = renderToStaticMarkup(createElement(ToolActivityList, { parts: input, backend: "claude", t, live: true }));
        assert.equal((html.match(/tool-activity-compound/g) || []).length, 1, fixture.id);
        assert.match(html, /class="tool-activity-summary" aria-expanded="false"/);
        assert.doesNotMatch(html, /tool-activity-static/);
        assert.doesNotMatch(html, /<button[^>]+class="tool-activity-item-summary|class="tool-detail"/);
      }
    } else {
      assert.equal(activities.length, 1, fixture.id);
      assert.ok(!activities[0].noDetails, fixture.id);
    }
    assert.equal(JSON.stringify(part), original, "provider input must remain unchanged");
  }
});

test("compound summaries deduplicate kinds and keep original calls and progress boundaries", () => {
  const command = tool("Bash", JSON.stringify({ command: "git status --short; cat browser-report.json; rg TODO src | head -20" }));
  const parts = [tool("Read", '{"file_path":"before.txt"}'), { ...command, id: "one" },
    { ...command, id: "two" }, message("Progress between calls", "commentary"), { ...command, id: "three" },
    tool("Read", '{"file_path":"after.txt"}')];
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts, backend: "claude", t, live: true }));
  assert.equal((html.match(/tool-activity-compound/g) || []).length, 3);
  assert.equal((html.match(/Ran command, read files/g) || []).length, 3);
  assert.match(html, /Read before.txt/); assert.match(html, /Read after.txt/);
  assert.doesNotMatch(html, /browser-report|rg TODO|tool-activity-static|read files, read files/);
  assert.match(html, /Progress between calls/);
  const sameKind = renderToStaticMarkup(createElement(ToolActivityList, { parts: [tool("shell", "cat a; cat b"), tool("shell", "cat c; cat d")], t, live: true }));
  assert.equal((sameKind.match(/tool-activity-compound/g) || []).length, 2);
});

test("derived command rows retain original IDs through out-of-order results", () => {
  const one = { ...tool("Bash", '{"command":"git status; cat a.txt"}'), id: "one" };
  const two = { ...tool("exec_command", '{"cmd":"rg TODO src | head -20"}'), id: "two" };
  const result = id => ({ role: "tool", kind: "tool_result", id, text: id + " output" });
  const activities = activitiesFromParts([one, two, result("two"), result("one")], "codex", t);
  assert.deepEqual(activities.map(a => [a.use.id, a.result.id, a.noDetails]), [["one", "one", true], ["one", "one", true], ["two", "two", true]]);
});

test("Codex and Claude tool classification matches server and native shared regression cases", () => {
  const cases = JSON.parse(readFileSync(new URL("../../android/app/src/test/resources/tool-classification.json", import.meta.url), "utf8"));
  const labels = { shell: "Ran command", list: "Listed files", read: "Read files", search: "Searched files", web: "Searched the web" };
  for (const fixture of cases) {
    const part = tool(fixture.name, fixture.input ? JSON.stringify(fixture.input) : fixture.text);
    const activity = displayActivity(part, "claude", t);
    assert.equal(activity.kind, fixture.kind, fixture.id);
    if (labels[fixture.kind] && !fixture.path && !fixture.query) assert.equal(activity.label, labels[fixture.kind], fixture.id);
    const projected = displayActivity({ ...part, text: "", tool: { kind: fixture.kind, path: fixture.path, query: fixture.query } }, "claude", t);
    if (fixture.kind !== "image") assert.equal(projected.label, activity.label, fixture.id + " lazy projection");
  }
});

test("Codex view_image and Claude Read images render grouped thumbnails with complete chat-scoped paths", () => {
  const path = "/work/build/artifacts/page 1.png";
  for (const name of ["view_image", "functions.view_image", "mcp__tools__view_image", "Read"]) {
    assert.deepEqual(viewedImagePaths(tool(name, JSON.stringify({ file_path: path }))), [path]);
  }
  assert.deepEqual(viewedImagePaths(tool("Read", '{"file_path":"main.go"}')), []);
  const parts = [tool("functions.view_image", JSON.stringify({ path })), tool("view_image", '{"path":"page2.png"}'), message("Checked both pages", "commentary"), tool("Read", '{"file_path":"page3.png"}')];
  const html = renderToStaticMarkup(createElement(LocalFileContext.Provider, { value: {
    imageURL: (target) => projectFileURL("project", target.path, "image", "chat"),
    previewImage() {},
  } }, createElement(ToolActivityList, { parts, backend: "codex", t, live: true })));
  assert.match(html, /Viewed 2 images/);
  assert.match(html, /Viewed 1 image/);
  assert.equal((html.match(/class="viewed-image"/g) || []).length, 3);
  assert.match(html, /src="\/api\/chats\/chat\/file\?path=%2Fwork%2Fbuild%2Fartifacts%2Fpage\+1.png&amp;image=1"/);
  assert.match(html, /object-fit|loading="lazy"/);
  assert.ok(html.indexOf("Viewed 2 images") < html.indexOf("Checked both pages"));
  assert.doesNotMatch(html, /<pre>|&quot;path&quot;|tool-detail/);
});

test("Claude parallel image results pair by tool_use_id and prefer persisted images over missing files", () => {
  const first = { ...tool("Read", '{"file_path":"first.png"}'), id: "one" };
  const second = { ...tool("Read", '{"file_path":"second.png"}'), id: "two" };
  const result = (id) => ({ role: "tool", kind: "tool_result", id, text: "", tool: { kind: "other", image_urls: [`/api/chats/chat/tool-image?ref=${id}&index=0`] } });
  const parts = [first, second, result("two"), result("one")];
  const activities = activitiesFromParts(parts, "claude", t);
  assert.equal(activities.length, 2);
  assert.equal(activities[0].result.id, "one");
  assert.equal(activities[1].result.id, "two");
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts, backend: "claude", t, live: true }));
  assert.match(html, /Viewed 2 images/);
  assert.equal((html.match(/<img /g) || []).length, 2);
  assert.match(html, /tool-image\?ref=one&amp;index=0/);
  assert.doesNotMatch(html, /disabled|<pre>|attachments/);
});

test("lazy image summaries and parallel batches retain thumbnails without loading raw tool bodies", () => {
  const batch = tool("multi_tool_use.parallel", JSON.stringify({ tool_uses: ["a.png", "b.png"].map((path) => ({ recipient_name: "functions.view_image", parameters: { path } })) }));
  assert.deepEqual(viewedImagePaths(batch), ["a.png", "b.png"]);
  const lazy = { ...batch, text: "", tool: { kind: "image", image_paths: ["a.png", "b.png"], href: "/chats/chat/tool-detail?ref=batch" } };
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts: [lazy], t, live: true }));
  assert.match(html, /Viewed 2 images/);
  assert.equal((html.match(/class="viewed-image"/g) || []).length, 2);
  assert.doesNotMatch(html, /Loading tool details|tool-detail-block|<pre>/);
  assert.deepEqual(viewedImagePaths(tool("Bash", '{"path":"a.png"}')), []);
});

test("unmatched image result IDs remain separate and remote or active image sources are never fetched", () => {
  const parts = [
    { ...tool("Read", '{"file_path":"notes.txt"}'), id: "text-call" },
    { role: "tool", kind: "tool_result", id: "other-call", text: "[image]", images: ["https://example.com/track.png", "data:image/svg+xml;base64,PHN2Zz4="] },
  ];
  const activities = activitiesFromParts(parts, "claude", t);
  assert.equal(activities.length, 2);
  assert.equal(activities[0].kind, "read");
  assert.equal(activities[0].result, undefined);
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts, backend: "claude", t, live: true }));
  assert.match(html, /Viewed 2 images/);
  assert.doesNotMatch(html, /<img|src=|https:\/\/example.com|data:image/);
});

test("compaction is only a live work label, never a historical status", () => {
  const turn = { status: "running", compacting: true, started_at: 1000 };
  assert.equal(turnWorkLabel(turn, t, 6000), "Compacting context…");
  assert.equal(turnWorkLabel(turn, t, 6000, true), "Compacting context…");
  assert.equal(turnWorkLabel({ ...turn, compacting: false }, t, 6000), "Working for 5s");
  assert.equal(turnWorkLabel({ ...turn, compacting: false }, t, 62000, true), "Worked for 1m 1s");
  for (const status of ["completed", "stopped", "failed", "interrupted"]) {
    assert.doesNotMatch(turnWorkLabel({ ...turn, status, finished_at: 6000 }, t, 9000), /Compacting/);
  }
});

test("thinking summaries keep their icon and full Markdown in a compact preview", () => {
  const title = "Designing the animation ".repeat(30).trim();
  const text = `**${title}**\n\nMore details with \`code\`.\n\n- Final step`;
  for (const part of [
    { role: "assistant", kind: "thinking", text },
    tool("reasoning", text),
  ]) {
    const html = renderToStaticMarkup(createElement(ToolActivityList, {
      parts: [part], backend: "codex", t, live: true,
    }));
    assert.match(html, /class="tool-activity-item-summary"[^>]*aria-expanded="false"/);
    assert.match(html, /lucide-brain/);
    assert.match(html, /class="tool-thinking-label-prefix">Think<\/span>/);
    assert.match(html, /class="markdown markdown-preview"/);
    assert.ok(html.includes(`<strong>${title}</strong>`));
    assert.match(html, /<code>code<\/code>/);
    assert.doesNotMatch(html, /class="tool-detail"|<p>|<ul>/);
    const activity = displayActivity(part, "codex", t);
    assert.equal(activity.detail, text);
    const detail = renderToStaticMarkup(createElement(ActivityDetail, { activity, t }));
    assert.match(detail, /class="tool-detail-label">Thought<\/span>/);
    assert.match(detail, /class="markdown"><p><strong>/);
    assert.ok(detail.includes(`<strong>${title}</strong>`));
    assert.match(detail, /<ul>\n<li>Final step<\/li>/);
    assert.doesNotMatch(detail, /markdown-preview/);
  }
});

test("progress messages stay between tools and do not merge tool groups across them", () => {
  const parts = [
    message("First progress", "commentary", "first"),
    tool("Read", '{"file_path":"a.txt"}'),
    { role: "tool", kind: "tool_result", text: "contents" },
    message("Second progress", "commentary", "second"),
    tool("Read", '{"file_path":"b.txt"}'),
    message(" \n", undefined, "ack"),
  ];
  const presentation = turnPresentation(parts, true);
  assert.equal(presentation.answer, "");
  assert.equal(presentation.expanded, true);
  const activities = activitiesFromParts(presentation.process, "claude", t);
  assert.deepEqual(activities.map((item) => item.kind), ["message", "read", "message", "read"]);
  assert.equal(activities[1].result.text, "contents");
  const html = renderToStaticMarkup(createElement(TurnResponse, {
    parts, backend: "claude", t, running: true, summary: "Working for 5s",
  }));
  const positions = ["First progress", "Read a.txt", "Second progress", "Read b.txt"].map((text) => html.indexOf(text));
  assert.ok(positions.every((position) => position >= 0));
  assert.deepEqual(positions, [...positions].sort((a, b) => a - b));
  assert.equal((html.match(/class="tool-progress-message"/g) || []).length, 2);
  assert.doesNotMatch(html, /class="assistant"/);
});

test("steering user inputs stay among tools, retain their files and do not interrupt call-result pairing", () => {
  const call = { ...tool("Read", '{"file_path":"a.txt"}'), id: "call" };
  const input = { role: "user", kind: "text", id: "follow", text: "Follow up" };
  const file = { role: "user", kind: "context", id: "follow", name: "notes.txt", file_path: "/work/notes.txt", text: "notes.txt" };
  const next = { role: "user", kind: "text", id: "next", text: "Next input" };
  const result = { role: "tool", kind: "tool_result", id: "call", text: "contents" };
  const parts = [call, input, file, next, result, tool("Read", '{"file_path":"b.txt"}')];
  const activities = activitiesFromParts(parts, "codex", t);
  assert.deepEqual(activities.map(activity => activity.kind), ["read", "user", "user", "read"]);
  assert.deepEqual(activities[1].input, [input, file]);
  assert.equal(activities[0].result, result);
  const legacy = activitiesFromParts([call, { ...input, id: undefined }, result, { ...next, id: undefined }], "codex", t);
  assert.deepEqual(legacy.map(activity => activity.kind), ["read", "user", "user"]);
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts, t, live: true,
    renderUser: input => createElement("p", {}, input.map(part => part.text).join(" / ")) }));
  const positions = ["Read a.txt", "Follow up / notes.txt", "Next input", "Read b.txt"].map(text => html.indexOf(text));
  assert.ok(positions.every(position => position >= 0));
  assert.deepEqual(positions, [...positions].sort((a,b) => a-b));
  assert.equal((html.match(/tool-progress-user/g) || []).length, 2);
  const closed = renderToStaticMarkup(createElement(TurnResponse, { parts: [...parts, message("Final", "final_answer")], t, running: false, summary: "Worked for 5s" }));
  assert.match(closed, /Final/);
  assert.doesNotMatch(closed, /Follow up|Next input|Read a.txt|tool-progress-user/);
});

test("final_answer collapses the process before turn completion and renders only the final outside it", () => {
  const progress = message("Hidden progress", "commentary", "progress");
  const final = message("**Final answer**", "final_answer", "final");
  const parts = [progress, tool("Read", '{"file_path":"a.txt"}'), final];
  for (const running of [true, false]) {
    const presentation = turnPresentation(parts, running);
    assert.deepEqual(presentation.process, parts.slice(0, 2));
    assert.equal(presentation.answer, final.text);
    assert.equal(presentation.expanded, false);
    const html = renderToStaticMarkup(createElement(TurnResponse, {
      parts, backend: "codex", t, running, summary: "Worked for 5s",
    }));
    assert.match(html, /aria-expanded="false"/);
    assert.match(html, /<strong>Final answer<\/strong>/);
    assert.doesNotMatch(html, /Hidden progress|Read a\.txt/);
    assert.equal((html.match(/Final answer/g) || []).length, 1);
  }
});

test("unphased Claude and legacy replies use trailing text only after the turn ends", () => {
  const progress = message("Checking", undefined, "progress");
  const final = message("Done", undefined, "final");
  const parts = [progress, tool("Read", '{"file_path":"a.txt"}'), final];
  assert.equal(turnPresentation(parts, true).answer, "");
  const complete = turnPresentation(parts, false);
  assert.equal(complete.answer, "Done");
  assert.deepEqual(complete.process, parts.slice(0, 2));
  const noAnswer = turnPresentation(parts.slice(0, 2), false);
  assert.equal(noAnswer.answer, "");
  assert.equal(noAnswer.expanded, false);
  assert.equal(turnPresentation([message("Progress only", "commentary")], false).answer, "");
});

test("answer chunks from the same message join without inventing spaces but separate messages retain boundaries", () => {
  const parts = [
    message("Hello ", "final_answer", "one"),
    message("world", "final_answer", "one"),
    message("Another message", "final_answer", "two"),
  ];
  assert.equal(turnPresentation(parts, false).answer, "Hello world\n\nAnother message");
  assert.equal(turnPresentation([message(" \n", "final_answer")], true).expanded, true);
});

test("tool output after a progress message does not become that message's result", () => {
  const parts = [tool("Read", '{"file_path":"a.txt"}'), message("Still working"), { role: "tool", kind: "tool_result", text: "output" }];
  const activities = activitiesFromParts(parts, "claude", t);
  assert.equal(activities[1].kind, "message");
  assert.equal(activities[1].result, undefined);
  assert.equal(activities[2].result.text, "output");
});

test("apply_patch is a named file activity even when its code mentions shell commands", () => {
  const patch = [
    "*** Begin Patch",
    "*** Add File: pong.html",
    "+<title>RALLY</title>",
    "+const controls = ['up', 'down'];",
    "+// find, cat, rg and websearch are part of this game, not tool calls",
    "*** End Patch",
  ].join("\n");
  const activity = displayActivity(tool("apply_patch", patch), "codex", t);
  assert.equal(activity.kind, "create");
  assert.equal(activity.label, "Created pong.html");
  assert.equal(activity.detail, patch);
  assert.equal(changeStats([activity]), "+3 -0");
});

function renderResult(text) {
  const [activity] = activitiesFromParts([
    tool("apply_patch", "update workspace files"),
    { role: "tool", kind: "tool_result", text },
  ], "codex", t);
  return renderToStaticMarkup(createElement(ActivityDetail, { activity, t }));
}

const filePatch = (name, old = "old", next = "new") =>
  `diff --git a/${name} b/${name}\n--- a/${name}\n+++ b/${name}\n@@ -1 +1 @@\n-${old}\n+${next}\n`;

const titlePatch = [
  "*** Begin Patch",
  "*** Update File: index.html",
  "@@",
  "-  <title>FALL LINE — 俄罗斯方块</title>",
  "+  <title>Spool · FALL LINE — 俄罗斯方块</title>",
  "*** Update File: gomoku.html",
  "@@",
  "-  <title>山海 · 五子棋</title>",
  "+  <title>Spool · 山海 · 五子棋</title>",
  "*** Update File: pong.html",
  "@@",
  "-  <title>RALLY · 乒乓球</title>",
  "+  <title>Spool · RALLY · 乒乓球</title>",
  "*** End Patch",
].join("\n");

const rawMarkup = (text) => renderToStaticMarkup(createElement("pre", null, text));
const renderInput = (text) => renderToStaticMarkup(createElement(ActivityDetail, {
  activity: displayActivity(tool("apply_patch", text), "codex", t), t,
}));

test("unnumbered apply_patch input renders all three title edits with original source available", () => {
  const files = toolResultDiffs(titlePatch);
  assert.deepEqual(files.map(({ name }) => name), ["index.html", "gomoku.html", "pong.html"]);
  assert.deepEqual(files.map(({ type }) => type), ["change", "change", "change"]);
  for (const file of files) {
    assert.equal(file.lineNumbersUnavailable, true);
    assert.equal(file.hunks.length, 1);
    assert.equal(file.hunks[0].hunkSpecs, undefined);
    assert.equal(file.deletionLines.length, 1);
    assert.equal(file.additionLines.length, 1);
    assert.ok(file.additionLines[0].includes("Spool · "));
  }
  const text = `\n${titlePatch}\n\n`;
  const html = renderInput(text);
  assert.equal((html.match(/class="tool-diff"/g) || []).length, 3);
  assert.match(html, /<summary>Raw input<\/summary>/);
  assert.ok(html.includes(rawMarkup(text)));
  assert.equal((renderResult(titlePatch).match(/class="tool-diff"/g) || []).length, 3);
});

test("apply_patch preserves separate context chunks, moves and literal file paths", () => {
  const oldName = 'folder/旧 "game".html';
  const newName = 'folder/new "game".html';
  const patch = [
    "*** Begin Patch",
    `*** Update File: ${oldName}`,
    `*** Move to: ${newName}`,
    "@@ function first()",
    " context",
    "-old",
    "+new",
    "@@ function last()",
    "-last",
    "+last edited",
    "",
    "*** End of File",
    "*** End Patch",
  ].join("\r\n");
  const [file] = toolResultDiffs(patch);
  assert.ok(file);
  assert.equal(file.name, newName);
  assert.equal(file.prevName, oldName);
  assert.equal(file.type, "rename-changed");
  assert.equal(file.hunks.length, 2);
  assert.deepEqual(file.hunks.map(({ hunkContext }) => hunkContext), ["function first()", "function last()"]);
  assert.deepEqual(file.additionLines, ["context\n", "new\n", "last edited\n", "\n"]);
  assert.deepEqual(file.deletionLines, ["context\n", "old\n", "last\n", "\n"]);
  assert.equal(file.hunks[1].noEOFCRAdditions, false);
  assert.equal(file.hunks[1].noEOFCRDeletions, false);
});

test("apply_patch supports additions, deletions, implicit hunks and empty new files", () => {
  const patch = [
    "*** Begin Patch",
    "*** Add File: a/file with spaces.txt",
    "+first",
    "+",
    "+last",
    "*** Add File: empty.txt",
    "*** Delete File: deleted.txt",
    "*** Update File: edited.txt",
    "-before",
    "+after",
    "*** End Patch",
  ].join("\n");
  const files = toolResultDiffs(patch);
  assert.deepEqual(files.map(({ name }) => name), ["a/file with spaces.txt", "empty.txt", "deleted.txt", "edited.txt"]);
  assert.deepEqual(files.map(({ type }) => type), ["new", "new", "deleted", "change"]);
  assert.deepEqual(files[0].additionLines, ["first\n", "\n", "last\n"]);
  assert.deepEqual(files[0].deletionLines, []);
  assert.equal(files[0].lineNumbersUnavailable, false);
  assert.equal(files[1].hunks.length, 0);
  assert.equal(files[2].contentsUnavailable, true);
  assert.deepEqual(files[2].deletionLines, []);
  assert.match(renderInput(patch), /File contents are not included in this patch\./);
});

test("malformed and incomplete apply_patch inputs fall back to the complete raw text", () => {
  for (const text of [
    titlePatch.replace("*** End Patch", ""),
    titlePatch.replace("*** Begin Patch", ""),
    titlePatch.replace("+  <title>Spool", "unmarked <title>Spool"),
    titlePatch.replace("*** Update File: gomoku.html", "*** Unknown File: gomoku.html"),
    "*** Begin Patch\n*** Add File: a.txt\n+first\nnot an addition\n*** End Patch",
    "*** Begin Patch\n*** Delete File: a.txt\n-old\n*** End Patch",
    "*** Begin Patch\n*** Update File: a.txt\n@@\n@@\n-old\n+new\n*** End Patch",
    "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End of File\n+extra\n*** End Patch",
    "*** Begin Patch\n*** Update File: a.txt\n@@\n context only\n*** End Patch",
    titlePatch + "\n*** Begin Patch\n*** Add File: unfinished.txt\n+not complete",
  ]) {
    assert.deepEqual(toolResultDiffs(text), []);
    const html = renderInput(text);
    assert.doesNotMatch(html, /class="tool-diff"/);
    assert.ok(html.includes(rawMarkup(text)));
  }
});

test("multi-file tool results render every parsed file instead of crashing PatchDiff", () => {
  const patch = ["a.js", "b.js", "c.js"].map((name) => filePatch(name)).join("");
  const files = toolResultDiffs(patch);
  assert.deepEqual(files.map(({ name }) => name), ["a.js", "b.js", "c.js"]);
  for (const file of files) {
    assert.deepEqual(file.additionLines, ["new\n"]);
    assert.deepEqual(file.deletionLines, ["old\n"]);
  }
  const html = renderResult(patch);
  assert.equal((html.match(/class="tool-diff"/g) || []).length, 3);
  assert.match(html, /<summary>Raw output<\/summary>/);
  assert.ok(html.includes(`<pre>${patch}</pre>`));
});

test("a single file and unified multi-file patches still render as file diffs", () => {
  for (const git of [true, false]) {
    const patch = ["a.txt", "b.txt"].map((name) => filePatch(name)).join("");
    const input = git ? patch : patch.replace(/^diff --git .*\n/gm, "");
    assert.equal(toolResultDiffs(input).length, 2);
    assert.equal((renderResult(input).match(/class="tool-diff"/g) || []).length, 2);
  }
  assert.equal(toolResultDiffs(filePatch("single.txt")).length, 1);
  assert.equal((renderResult(filePatch("single.txt")).match(/class="tool-diff"/g) || []).length, 1);
});

test("unsupported, partial and malformed patch results retain the complete raw output", () => {
  for (const text of [
    "Success. Updated the following files:\nA pong.html",
    "diff --git a/a.js b/a.js\n--- a/a.js\n+++ b/a.js\n@@ -2,3 +2,3 @@\n-partial\n",
    filePatch("a.js") + "diff --git invalid header\n@@ -1 +1 @@\n-old\n+new\n",
    "--- a/a.js\n+++ b/a.js\n@@ not a hunk @@\n-full source\n+still present\n",
  ]) {
    assert.deepEqual(toolResultDiffs(text), []);
    const html = renderResult(text);
    assert.doesNotMatch(html, /class="tool-diff"/);
    const escaped = text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
    assert.ok(html.includes(`<pre>${escaped}</pre>`));
  }
});

test("diff parsing preserves the final whitespace-only context line and surrounding output", () => {
  const patch = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n-old\n+new\n \n";
  const text = "Exit code: 0\nOutput:\n" + patch;
  const [file] = toolResultDiffs(text);
  assert.ok(file);
  assert.deepEqual(file.additionLines, ["new\n", "\n"]);
  assert.deepEqual(file.deletionLines, ["old\n", "\n"]);
  const html = renderResult(text);
  assert.match(html, /class="tool-diff"/);
  assert.ok(html.includes(`<pre>${text}</pre>`));
});

test("a renderer failure falls back to complete output instead of removing the conversation", () => {
  const raw = filePatch("fallback.txt");
  const boundary = new ToolDiffBoundary({ raw, children: createElement("div", null, "rendered diff") });
  assert.equal(boundary.state.failed, false);
  boundary.state = ToolDiffBoundary.getDerivedStateFromError(new Error("renderer failure"));
  const html = renderToStaticMarkup(boundary.render());
  assert.equal(html, `<pre>${raw}</pre>`);
});

test("patch headers determine edit/delete classification and paths with spaces", () => {
  for (const [name, action, kind, label] of [
    ["functions.apply_patch", "Update", "write", "Edited folder/my game.html"],
    ["apply_patch", "Delete", "delete", "Deleted folder/my game.html"],
  ]) {
    const activity = displayActivity(
      tool(name, `*** Begin Patch\n*** ${action} File: folder/my game.html\n-cat\n+find\n*** End Patch`),
      "codex",
      t,
    );
    assert.equal(activity.kind, kind);
    assert.equal(activity.label, label);
  }
  const mixed = displayActivity(
    tool("apply_patch", "*** Begin Patch\n*** Add File: a.html\n+new\n*** Update File: b.html\n-old\n+new\n*** End Patch"),
    "codex",
    t,
  );
  assert.equal(mixed.kind, "write");
  assert.equal(mixed.label, "Edited files");
});

test("edit and write tools are not classified from words in source code", () => {
  for (const [name, kind] of [["Edit", "write"], ["Write", "create"]]) {
    const activity = displayActivity(
      tool(name, JSON.stringify({ file_path: "pong.html", content: "controls.find cat websearch" })),
      "claude",
      t,
    );
    assert.equal(activity.kind, kind);
    assert.match(activity.label, /pong\.html$/);
  }
  const shell = displayActivity(tool("exec_command", '{"cmd":"ls -la"}'), "codex", t);
  assert.equal(shell.kind, "list");
});

test("converted patch input and result remain in expandable tool details in full", () => {
  const patch = "*** Begin Patch\n*** Add File: pong.html\n" + "+<p>complete game line</p>\n".repeat(25_000) + "*** End Patch";
  const [file] = toolResultDiffs(patch);
  assert.equal(file.additionLines.length, 25_000);
  assert.equal(file.hunks[0].additionLines, 25_000);
  assert.equal(file.additionLines.at(-1), "<p>complete game line</p>\n");
  const output = "Success. Updated the following files:\nA pong.html";
  const parts = [tool("apply_patch", patch), { role: "tool", kind: "tool_result", text: output }];
  const activities = activitiesFromParts(parts, "codex", t);
  assert.equal(activities.length, 1);
  assert.equal(activities[0].label, "Created pong.html");
  assert.equal(activities[0].use.text, patch);
  assert.equal(activities[0].result.text, output);
  assert.equal(changeStats(activities), "+25000 -0");
  const html = renderToStaticMarkup(createElement(ActivityDetail, { activity: activities[0], t }));
  assert.match(html, /\*\*\* Add File: pong\.html/);
  assert.match(html, /\*\*\* End Patch/);
  assert.match(html, /Success\. Updated the following files:/);
  assert.match(html, /Command · apply_patch/);
  assert.match(html, /<summary>Raw input<\/summary>/);
  assert.equal((html.match(/class="tool-diff"/g) || []).length, 1);
  assert.equal((html.match(/complete game line/g) || []).length, 25_000);
  const list = renderToStaticMarkup(
    createElement(ToolActivityList, { parts, backend: "codex", t, live: true, summary: "Worked for 1m 52s" }),
  );
  assert.match(list, /Created pong\.html/);
  assert.match(list, /title="apply_patch"/);
  assert.match(list, /\+25000 -0/);
});
