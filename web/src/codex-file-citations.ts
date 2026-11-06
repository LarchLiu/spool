import type { Root } from "mdast";
import type { Extension as FromMarkdownExtension } from "mdast-util-from-markdown";
import type { Tokenizer } from "micromark-util-types";
import type { Plugin } from "unified";
import type {} from "remark-parse";
import { localFileTarget } from "./local-file-links.js";

declare module "micromark-util-types" {
  interface TokenTypeMap {
    codexFileCitation: "codexFileCitation";
  }
}

const marker = ":codex-file-citation{";

function citation(source: string) {
  const attributes = source.slice(marker.length, -1);
  const attribute = /[ \t]*([A-Za-z_][\w-]*)=("(?:[^"\\\r\n]|\\.)*")/y;
  const values = new Map<string, string>();
  let cursor = 0;
  while (cursor < attributes.length) {
    if (/^[ \t]*$/.test(attributes.slice(cursor))) break;
    attribute.lastIndex = cursor;
    const match = attribute.exec(attributes);
    if (!match || values.has(match[1])) return null;
    try {
      values.set(match[1], JSON.parse(match[2]));
    } catch {
      return null;
    }
    cursor = attribute.lastIndex;
    if (cursor < attributes.length && !/[ \t]/.test(attributes[cursor]))
      return null;
  }
  const path = values.get("path");
  if (!path || !localFileTarget(path)) return null;
  return { path, name: path.replaceAll("\\", "/").split("/").pop() || path };
}

const tokenize: Tokenizer = function (effects, ok, nok) {
  const context = this;
  let source = "";
  let quoted = false;
  let escaped = false;
  return start;

  function start(code: number | null) {
    effects.enter("codexFileCitation");
    return inside(code);
  }

  function inside(code: number | null): ReturnType<Tokenizer> | undefined {
    if (code === null || code < -2 || code === 10 || code === 13)
      return nok(code);
    const char =
      code === -2 ? "\t" : code === -1 ? "" : String.fromCharCode(code);
    if (source.length < marker.length && char !== marker[source.length])
      return nok(code);
    source += char;
    effects.consume(code);
    // Keep explicit Markdown link/image labels intact and avoid nested controls.
    if (
      source === marker &&
      context.events.some(
        ([, token]) =>
          (token.type === "labelLink" || token.type === "labelImage") &&
          !token._balanced,
      )
    )
      return nok;
    if (source.length <= marker.length) return inside;
    if (escaped) escaped = false;
    else if (quoted && char === "\\") escaped = true;
    else if (char === '"') quoted = !quoted;
    else if (!quoted && char === "}") {
      if (!citation(source)) return nok;
      effects.exit("codexFileCitation");
      return ok;
    }
    return inside;
  }
};

const fromMarkdown: FromMarkdownExtension = {
  enter: {
    codexFileCitation(token) {
      const file = citation(this.sliceSerialize(token))!;
      this.enter(
        {
          type: "link",
          url: file.path.replaceAll("\\", "/"),
          title: null,
          children: [{ type: "text", value: file.name }],
        },
        token,
      );
    },
  },
  exit: {
    codexFileCitation(token) {
      this.exit(token);
    },
  },
};

// An inline syntax extension leaves code, escaped colons and HTML untouched.
export const remarkCodexFileCitations: Plugin<[], Root> = function () {
  const data = this.data();
  (data.micromarkExtensions ||= []).push({
    text: { 58: { name: "codexFileCitation", tokenize } },
  });
  (data.fromMarkdownExtensions ||= []).push(fromMarkdown);
};
