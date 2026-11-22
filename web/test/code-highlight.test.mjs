import { test } from "node:test";
import assert from "node:assert/strict";
import { highlightCode } from "../.test-build/code-highlight.js";

test("fenced languages and aliases use shared syntax grammars in both themes", async () => {
  for (const [language, source] of [
    ["JS", 'const message = "Hello"; // comment\n'],
    ["ts", 'export const message: string = "Hello";\n'],
    ["python", 'def greet(name):\n\treturn "Hello" # comment\n'],
    ["c++", "int main() { return 42; } // comment\n"],
    ["json", '{"message": "Hello", "enabled": true}\n'],
    ["sh", '#!/bin/sh\necho "Hello"\n'],
    ["html", '<script>alert("Hello")</script>\n'],
    ["mermaid", 'flowchart LR\n %% comment\n A["开始"] --> B{Ready?}\n'],
  ]) {
    const tokens = (await highlightCode(source, language))?.flat();
    assert.ok(tokens?.length, language);
    for (const theme of ["light", "dark"]) {
      assert.ok(
        new Set(tokens.map((token) => token.htmlStyle[`--code-${theme}`]))
          .size > 1,
        `${language}/${theme} has syntax colors`,
      );
    }
    for (const token of tokens)
      assert.equal(
        source.slice(token.offset, token.offset + token.content.length),
        token.content,
      );
  }
});

test("plain and unknown languages fall back without guessing syntax", async () => {
  for (const language of ["", "text", "TXT", "plaintext", "not-a-language"])
    assert.equal(await highlightCode('const value = "plain";', language), null);
  assert.equal(await highlightCode("", "js"), null);
});

test("partial streamed code, Unicode and long lines retain source offsets", async () => {
  const source =
    '\tconst value = "你好😀";  \r\n\n' +
    "x".repeat(1001) +
    '\nreturn "unfinished';
  const lines = await highlightCode(source, "ts");
  assert.equal(lines.length, 4);
  for (const token of lines.flat())
    assert.equal(
      source.slice(token.offset, token.offset + token.content.length),
      token.content,
    );
  assert.ok(lines.at(-1).length, "the partial final line is tokenized");
});

test("cancelled grammar requests do not return stale tokens", async () => {
  const controller = new AbortController();
  const pending = highlightCode(
    'const value = "old";',
    "ts",
    controller.signal,
  );
  controller.abort();
  assert.equal(await pending, null);
});
