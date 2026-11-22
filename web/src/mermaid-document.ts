// Both clients run the same classic scripts in an isolated document. Diagram
// text arrives as data; it is never interpolated into HTML or executable code.
// Web uses external scripts to honor the parent page's script-src 'self' CSP;
// Android embeds the scripts in its offline document.
export function mermaidDocument(
  scripts: readonly ({ src: string } | { code: string })[],
): string {
  const tags = scripts.map((script) =>
    "src" in script
      ? `<script nonce="spool-mermaid" src="${script.src.replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;")}"></script>`
      : `<script nonce="spool-mermaid">${script.code.replace(/<\/script/gi, "<\\/script")}</script>`,
  );
  return `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'nonce-spool-mermaid'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'">
<style>html,body{margin:0;background:transparent}body{font:14px system-ui,sans-serif}#diagram{padding:12px 16px 16px;overflow:auto}#diagram>svg{display:block;margin:auto}#scratch{position:absolute;visibility:hidden;pointer-events:none;inset:0 auto auto 0;width:100%}</style>
</head><body><div id="diagram"></div><div id="scratch"></div>
${tags.join("\n")}</body></html>`;
}
