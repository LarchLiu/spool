import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ReleaseHistoryLoader } from "../.test-build/release-history.js";
import { ReleaseHistoryVersions, ReleaseNotesPanel } from "../.test-build/components/web/src/release-notes.js";

const t = (key, values = {}) => key.replace(/\{([^}]+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));
const history = {
  current_version: "1.0.0", target_version: "1.2.0",
  releases: [{ version: "1.2.0", name: "Latest release", body: "## Features\n\n- **Bold** and `code`\n- [x] Complete\n\n| Feature | Status |\n| --- | --- |\n| Markdown | Yes |\n\n```js\nconst ok = true;\n```\n\n[Compare](../../compare/v1.0.0...v1.2.0)", url: "https://github.com/owner/spool/releases/tag/v1.2.0", published_at: "2026-10-29T01:00:00Z", prerelease: false },
    { version: "1.1.0-beta.1", name: "v1.1.0-beta.1", body: "", url: "https://github.com/owner/spool/releases/tag/v1.1.0-beta.1", published_at: "2026-10-28T01:00:00Z", prerelease: true }],
};

test("release history deduplicates loading, caches success and refreshes explicitly", async () => {
  let resolve, calls = 0, notices = 0;
  const loader = new ReleaseHistoryLoader("1.0.0", "1.2.0", () => { calls++; return new Promise((done) => { resolve = done; }); });
  const unsubscribe = loader.subscribe(() => notices++);
  const first = loader.load();
  assert.equal(loader.getState().loading, true);
  assert.equal(loader.load(), first);
  await Promise.resolve();
  assert.equal(calls, 1);
  resolve(history);
  await first;
  assert.equal(loader.getState().history, history);
  assert.equal(loader.getState().loading, false);
  await loader.load();
  assert.equal(calls, 1);
  const refresh = loader.load(true);
  await Promise.resolve();
  assert.equal(calls, 2);
  resolve(history);
  await refresh;
  assert.equal(notices, 4);
  unsubscribe();
});

test("release history retries failures, preserves cached content and rejects stale ranges", async () => {
  let result = new Error("offline");
  const loader = new ReleaseHistoryLoader("1.0.0", "1.2.0", async () => { if (result instanceof Error) throw result; return result; });
  await loader.load();
  assert.equal(loader.getState().error, "offline");
  result = history;
  await loader.load();
  assert.equal(loader.getState().error, "");
  result = { ...history, target_version: "1.3.0" };
  await loader.load(true);
  assert.match(loader.getState().error, /available update changed/);
  assert.equal(loader.getState().history, history);
  result = { ...history, current_version: "0.9.0" };
  const fresh = new ReleaseHistoryLoader("1.0.0", "1.2.0", async () => result);
  await fresh.load();
  assert.equal(fresh.getState().history, undefined);
  assert.match(fresh.getState().error, /available update changed/);
});

test("each version renders its own heading, date, GitHub link and GFM Markdown", () => {
  const html = renderToStaticMarkup(createElement(ReleaseHistoryVersions, { history, t }));
  assert.equal((html.match(/<article/g) ?? []).length, 2);
  assert.match(html, /<h3>v1\.2\.0<\/h3>/);
  assert.match(html, /<h3>v1\.1\.0-beta\.1<\/h3>/);
  assert.match(html, /<time dateTime="2026-10-29T01:00:00Z">2026-10-29<\/time>/);
  assert.match(html, /<h2>Features<\/h2>/);
  assert.match(html, /<strong>Bold<\/strong>/);
  assert.match(html, /<code class="language-js">const ok = true;/);
  assert.match(html, /class="markdown-table"><table>/);
  assert.match(html, /type="checkbox" disabled="" checked=""/);
  assert.match(html, /href="https:\/\/github.com\/owner\/spool\/compare\/v1.0.0\.\.\.v1.2.0"/);
  assert.match(html, /target="_blank" rel="noopener noreferrer"/);
  assert.match(html, /Pre-release/);
  assert.match(html, /No release notes were provided/);
  assert.equal((html.match(/Latest release/g) ?? []).length, 1);
  assert.doesNotMatch(html, /settings-release-name">v1.1.0/);
});

test("release Markdown never runs HTML, unsafe links or remote image loads", () => {
  const body = '<script>alert(1)</script>\n\n<iframe src="https://evil.test"></iframe>\n\n[Bad](javascript:alert%281%29)\n\n[File](file:///etc/passwd)\n\n![Preview](https://example.com/photo.png)\n\n[Safe](https://github.com/owner/spool)';
  const html = renderToStaticMarkup(createElement(ReleaseHistoryVersions, { history: { ...history, releases: [{ ...history.releases[0], body }] }, t }));
  assert.doesNotMatch(html, /<script|<iframe|<img|javascript:|file:\/\/\/|onerror/);
  assert.match(html, /<span>Bad<\/span>/);
  assert.match(html, /href="https:\/\/example.com\/photo.png"[^>]*>Preview<\/a>/);
  assert.match(html, /href="https:\/\/github.com\/owner\/spool"/);
});

test("release history starts collapsed and identifies the exact version range", () => {
  const html = renderToStaticMarkup(createElement(ReleaseNotesPanel, { current: "1.0.0", target: "1.2.0", t }));
  assert.match(html, /aria-expanded="false"/);
  assert.match(html, /View release notes/);
  assert.match(html, /From 1.0.0 to 1.2.0/);
  assert.doesNotMatch(html, /Loading releases|settings-release-body/);
  const empty = renderToStaticMarkup(createElement(ReleaseHistoryVersions, { history: { ...history, releases: [] }, t }));
  assert.match(empty, /No releases were found/);
});
