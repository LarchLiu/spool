import type { ProjectFile } from "./types.js";

export function filePreviewKind(
  file: Pick<ProjectFile, "kind" | "name" | "media_type">,
): "markdown" | "html" | null {
  if (file.kind !== "text") return null;
  if (/\.md(?:own|x)?$/i.test(file.name)) return "markdown";
  if (
    /\.html?$/i.test(file.name) ||
    file.media_type.split(";")[0].trim().toLowerCase() === "text/html"
  )
    return "html";
  return null;
}

const htmlPreviewPolicy = [
  "default-src 'none'",
  "style-src 'unsafe-inline'",
  "img-src data:",
  "base-uri 'none'",
  "form-action 'none'",
].join("; ");

export function HtmlFilePreview({
  contents,
  title,
}: {
  contents: string;
  title: string;
}) {
  // Enforce the policy before parsing any file content, even a full HTML document.
  const document = `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${htmlPreviewPolicy}"><meta name="referrer" content="no-referrer">${contents}`;
  return (
    <iframe
      className="file-html-preview"
      title={title}
      sandbox=""
      referrerPolicy="no-referrer"
      srcDoc={document}
    />
  );
}
