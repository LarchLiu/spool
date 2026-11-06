import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
Object.defineProperty(globalThis, "navigator", {
  value: { languages: ["en"] },
  configurable: true,
});
const { ApprovalCard, approvalContent } =
  await import("../src/approval-card.tsx");

const render = (request) =>
  renderToStaticMarkup(
    createElement(ApprovalCard, {
      request: { id: "approval", title: "Computer Use", ...request },
      chatID: "chat",
      turnID: "turn",
      t: (key) => key,
      onError: () => {},
    }),
  );

test("Computer Use approval shows the requested app and both decisions", () => {
  const html = render({
    reason: 'Allow Computer Use to use "Calculator"?',
    detail: "App: Calculator",
  });
  assert.match(html, /Computer Use/);
  assert.match(html, /Calculator/);
  assert.match(html, /type="button"[^>]*>Deny/);
  assert.match(html, /type="submit"[^>]*>Allow once/);
  assert.doesNotMatch(html, /disabled/);
});

test("MCP inputs preserve boolean false, numeric types, multiple choices and optional omission", () => {
  const fields = [
    { name: "confirm", type: "boolean" },
    { name: "name", type: "string", required: true },
    { name: "count", type: "integer" },
    { name: "apps", type: "array" },
    { name: "optional", type: "string" },
  ];
  const data = new FormData();
  data.set("name", "Alex");
  data.set("count", "2");
  data.append("apps", "calc");
  data.append("apps", "notes");
  data.set("optional", "");
  assert.deepEqual(approvalContent(fields, data), {
    confirm: false,
    name: "Alex",
    count: 2,
    apps: ["calc", "notes"],
  });
  data.set("confirm", "on");
  assert.equal(approvalContent(fields, data).confirm, true);
});

test("forms expose labels, required input, constraints and choices", () => {
  const html = render({
    fields: [
      {
        name: "confirm",
        label: "Confirm app",
        type: "boolean",
        required: true,
      },
      {
        name: "count",
        label: "Count",
        type: "integer",
        required: true,
        minimum: 1,
        maximum: 4,
      },
      {
        name: "app",
        label: "App",
        type: "string",
        options: [{ value: "calc", label: "Calculator" }],
      },
    ],
  });
  assert.match(html, /Confirm app/);
  assert.match(html, /name="confirm"/);
  assert.doesNotMatch(html, /type="checkbox"[^>]*required/);
  assert.match(html, /min="1"/);
  assert.match(html, /max="4"/);
  assert.match(html, /step="1"/);
  assert.match(html, /class="custom-select-trigger"/);
  assert.match(html, /aria-label="App"/);
  assert.doesNotMatch(html, /<select|<option/);
});

test("URL authorization is a user-opened link with a separate confirmation", () => {
  const html = render({ url: "https://example.com/authorize" });
  assert.match(html, /href="https:\/\/example.com\/authorize"/);
  assert.match(html, /rel="noopener noreferrer"/);
  assert.match(html, /Confirm authorization/);
  assert.match(html, /Complete authorization on the linked page/);
});

test("unsupported verification remains visible with a working deny action", () => {
  const html = render({
    unavailable: "This request requires device verification in the Codex app.",
  });
  assert.match(html, /requires device verification/);
  assert.match(html, /type="submit"[^>]*disabled/);
  assert.match(html, /type="button" class="quiet">Deny/);
});
