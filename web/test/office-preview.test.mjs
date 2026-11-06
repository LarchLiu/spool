import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { OfficePage, officeColumn } from "../.test-build/components/web/src/office-preview.js";

const fixture = JSON.parse(await readFile(new URL("../../android/app/src/test/resources/office-preview.json", import.meta.url), "utf8"));
const catalog = JSON.parse(await readFile(new URL("../../locales/zh-CN.json", import.meta.url), "utf8"));
const t = key => catalog[key] || key;

test("actual Go Word preview renders headings, escaped text, images and merged tables", () => {
  const html = renderToStaticMarkup(createElement(OfficePage, { document: fixture.word, t }));
  assert.match(html, /<h1[^>]*><span[^>]*>Office 预览/);
  assert.match(html, /font-weight:700/);
  assert.match(html, /&lt;script&gt;literal&lt;\/script&gt;/);
  assert.match(html, /src="data:image\/png;base64,/);
  assert.match(html, /colSpan="2"/i);
  assert.doesNotMatch(html, /<script|https:\/\/example/);
});

test("actual Go Excel pages retain formatted values, merges and row numbering", () => {
  const first = renderToStaticMarkup(createElement(OfficePage, { document: fixture.sheets[0], t }));
  assert.match(first, /\$2\.50/); assert.match(first, /=SUM\(A2:B2\)/);
  assert.match(first, /colSpan="2"/i);
  const next = renderToStaticMarkup(createElement(OfficePage, { document: fixture.sheets[1], t }));
  assert.match(next, /scope="row">51/); assert.match(next, /rowSpan="2"/i);
  assert.match(renderToStaticMarkup(createElement(OfficePage, { document: fixture.sheets[3], t })), /此工作表为空/);
  for (const [index, name] of [[0, "A"], [25, "Z"], [26, "AA"], [127, "DX"]]) assert.equal(officeColumn(index), name);
});

test("Office text and styles cannot add markup, external images or arbitrary CSS", () => {
  const html = renderToStaticMarkup(createElement(OfficePage, { t, document: { kind: "word", blocks: [{ kind: "paragraph", runs: [{ text: '<img src="https://example.invalid">', style: { color: 'red; background:url(https://example.invalid)' } }, { image: 'https://example.invalid/tracker', text: "external" }] }] } }));
  assert.doesNotMatch(html, /<img|background:url|style="/);
  assert.match(html, /&lt;img/);
});
