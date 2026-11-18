import type { ThemedToken } from "@pierre/diffs";

export async function highlightCode(
  source: string,
  language: string,
  signal?: AbortSignal,
) {
  const lang = language.toLowerCase();
  if (!source || /^(?:text|txt|plaintext)?$/.test(lang)) return null;
  try {
    // Share the lazily loaded grammars and Pierre themes with file previews.
    const { getSharedHighlighter } = await import("@pierre/diffs");
    const highlighter = await getSharedHighlighter({
      themes: ["pierre-light", "pierre-dark"],
      langs: [lang],
    });
    if (signal?.aborted) return null;
    return highlighter.codeToTokens(source, {
      lang,
      themes: { light: "pierre-light", dark: "pierre-dark" },
      defaultColor: false,
      cssVariablePrefix: "--code-",
      tokenizeMaxLineLength: 1000,
      tokenizeTimeLimit: 0,
    }).tokens;
  } catch {
    // Unknown languages and unavailable grammar chunks remain readable text.
    return null;
  }
}

/** Decorate the text without changing its bytes, scroll container, or selection. */
export function applyCodeHighlight(
  code: HTMLElement,
  source: string,
  lines: ThemedToken[][] | null,
) {
  if (code.textContent !== source) return;
  if (!lines && !code.querySelector(".markdown-code-token")) return;
  const doc = code.ownerDocument;
  const selection = doc.getSelection();
  const anchorNode = selection?.anchorNode;
  const focusNode = selection?.focusNode;
  const anchorOffset = selection?.anchorOffset ?? 0;
  const focusOffset = selection?.focusOffset ?? 0;
  const offset = (node: Node | null, at: number) => {
    if (!node || !code.contains(node)) return null;
    const range = doc.createRange();
    range.selectNodeContents(code);
    range.setEnd(node, at);
    return range.toString().length;
  };
  const anchor = offset(anchorNode ?? null, anchorOffset);
  const focus = offset(focusNode ?? null, focusOffset);
  const fragment = doc.createDocumentFragment();
  let end = 0;
  for (const line of lines ?? []) {
    for (const token of line) {
      if (token.offset > end) fragment.append(source.slice(end, token.offset));
      const span = doc.createElement("span");
      span.className = "markdown-code-token";
      span.textContent = source.slice(
        token.offset,
        token.offset + token.content.length,
      );
      for (const [property, value] of Object.entries(token.htmlStyle ?? {}))
        span.style.setProperty(property, value);
      fragment.append(span);
      end = token.offset + token.content.length;
    }
  }
  if (end < source.length) fragment.append(source.slice(end));
  // React owns the single source-text child; source updates replace these spans.
  code.replaceChildren(fragment);
  if (
    selection &&
    anchorNode &&
    focusNode &&
    (anchor !== null || focus !== null)
  ) {
    const position = (at: number): [Node, number] => {
      const walker = doc.createTreeWalker(code, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode())) {
        const length = node.textContent?.length ?? 0;
        if (at <= length) return [node, at];
        at -= length;
      }
      return [code, code.childNodes.length];
    };
    const from: [Node, number] =
      anchor !== null ? position(anchor) : [anchorNode, anchorOffset];
    const to: [Node, number] =
      focus !== null ? position(focus) : [focusNode, focusOffset];
    selection.setBaseAndExtent(...from, ...to);
  }
}
