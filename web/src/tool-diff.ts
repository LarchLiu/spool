import { parsePatchFiles, processFile, type FileDiffMetadata } from "@pierre/diffs";

export type ToolFileDiff = FileDiffMetadata & {
  lineNumbersUnavailable?: boolean;
  contentsUnavailable?: boolean;
};

export function patchText(value: string) {
  const start = value.search(/(?:^|\n)(?:diff --git |--- [^\n]+\n\+\+\+ |\*\*\* Begin Patch)/);
  if (start < 0) return "";
  const patch = value.slice(start).replace(/^\n/, "");
  return patch.includes("@@") || patch.includes("*** Begin Patch") ? patch : "";
}

type PatchChunk = { context: string; lines: string[] };

function applyPatchDiffs(patch: string): ToolFileDiff[] {
  const lines = patch.replaceAll("\r\n", "\n").trimEnd().split("\n");
  if (lines[0] !== "*** Begin Patch" || lines.at(-1) !== "*** End Patch") return [];
  const files: ToolFileDiff[] = [];
  let index = 1;
  while (index < lines.length - 1) {
    const header = lines[index++].match(/^\*\*\* (Add|Update|Delete) File: (.+)$/);
    if (!header) return [];
    const [, action, name] = header;
    let nextName = name;
    if (action === "Update" && lines[index]?.startsWith("*** Move to: ")) {
      nextName = lines[index++].slice("*** Move to: ".length);
      if (!nextName.trim()) return [];
    }
    const chunks: PatchChunk[] = [];
    let chunk: PatchChunk | undefined;
    let atEOF = false;
    while (index < lines.length - 1 && !/^\*\*\* (?:Add|Update|Delete) File: /.test(lines[index])) {
      const line = lines[index++];
      if (atEOF || action === "Delete") return [];
      if (action === "Update" && (line === "@@" || line.startsWith("@@ "))) {
        if (chunk && !chunk.lines.length) return [];
        chunk = { context: line.slice(2).trimStart(), lines: [] };
        chunks.push(chunk);
      } else if (action === "Update" && line === "*** End of File") {
        if (!chunk?.lines.length) return [];
        // This anchors the match at EOF; it does not mean "no final newline".
        atEOF = true;
      } else {
        const body = action === "Update" && line === "" ? " " : line;
        if (!(action === "Add" ? body.startsWith("+") : /^[ +\-]/.test(body))) return [];
        if (!chunk) {
          chunk = { context: "", lines: [] };
          chunks.push(chunk);
        }
        chunk.lines.push(body);
      }
    }
    if (action === "Update" && (
      !chunk?.lines.length ||
      !chunks.some(({ lines }) => lines.some((line) => /^[+\-]/.test(line)))
    )) return [];

    // Only the renderer needs numeric hunk ranges. These are relative to the
    // supplied snippets, not source positions, so update line numbers are hidden.
    const unified = ["diff --git a/patch b/patch"];
    let oldOffset = 0;
    let newOffset = 0;
    for (const chunk of chunks) {
      let oldCount = 0;
      let newCount = 0;
      for (const line of chunk.lines) {
        if (!line.startsWith("+")) oldCount++;
        if (!line.startsWith("-")) newCount++;
      }
      unified.push(`@@ -${oldOffset + (oldCount ? 1 : 0)},${oldCount} +${newOffset + (newCount ? 1 : 0)},${newCount} @@ ${chunk.context}`);
      for (const line of chunk.lines) unified.push(line);
      oldOffset += oldCount + 1;
      newOffset += newCount + 1;
    }
    // Parse a fixed header and assign paths afterwards: user paths can contain
    // spaces, quotes or Unicode that must not become patch syntax.
    const file = processFile(unified.join("\n") + "\n", { throwOnError: true });
    if (!file) return [];
    file.name = nextName;
    file.prevName = name !== nextName ? name : undefined;
    file.type = action === "Add" ? "new"
      : action === "Delete" ? "deleted"
      : name !== nextName ? "rename-changed" : "change";
    if (action === "Update") {
      for (const hunk of file.hunks) hunk.hunkSpecs = undefined;
    }
    files.push({
      ...file,
      lineNumbersUnavailable: action === "Update",
      contentsUnavailable: action === "Delete",
    });
  }
  return files;
}

export function toolResultDiffs(value: string): ToolFileDiff[] {
  const patch = patchText(value);
  if (!patch) return [];
  try {
    if (patch.startsWith("*** Begin Patch")) return applyPatchDiffs(patch);
    // Best-effort recovery can silently discard incomplete hunks.
    return parsePatchFiles(patch, undefined, true).flatMap(({ files }) => files);
  } catch {
    return [];
  }
}
