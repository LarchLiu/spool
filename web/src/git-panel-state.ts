export type GitFile = {
  path: string;
  previous_path?: string;
  status: string;
  staged: boolean;
  unstaged?: boolean;
  staged_additions?: number;
  staged_deletions?: number;
  unstaged_additions?: number;
  unstaged_deletions?: number;
  untracked: boolean;
  additions: number;
  deletions: number;
  binary?: boolean;
};
export type GitCommit = {
  id: string;
  subject: string;
  message?: string;
  author: string;
  time: number;
  tags?: string[];
  unpushed?: boolean;
};
export type GitView = {
  repository: boolean;
  branch?: string;
  commit?: string;
  files: GitFile[];
  commits: GitCommit[];
  truncated?: boolean;
  writable?: boolean;
  index?: string;
  remote?: string;
  remote_branch?: string;
  upstream?: string;
  ahead?: number;
  behind?: number;
  remote_allowed?: boolean;
  last_commit?: GitCommit;
  selected_commit?: GitCommit;
  next_cursor?: string;
};
export type GitDiff = {
  path: string;
  patch: string;
  binary: boolean;
  truncated: boolean;
};
export type DiffLine = {
  text: string;
  kind: "add" | "delete" | "context" | "meta";
  old?: number;
  next?: number;
};

// Unified rows preserve source whitespace and both actual source line numbers.
export function unifiedDiffLines(patch: string): DiffLine[] {
  let old = 0,
    next = 0,
    inHunk = false;
  const rows: DiffLine[] = [];
  patch
    .replace(/\n$/, "")
    .split("\n")
    .forEach((text) => {
      const hunk = text.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (hunk) {
        if (rows.length) rows.push({ text: "⋯", kind: "meta" });
        old = Number(hunk[1]);
        next = Number(hunk[2]);
        inHunk = true;
      } else if (text.startsWith("diff --git ")) inHunk = false;
      else if (inHunk) {
        if (text.startsWith("+"))
          rows.push({ text: text.slice(1), kind: "add", next: next++ });
        else if (text.startsWith("-"))
          rows.push({ text: text.slice(1), kind: "delete", old: old++ });
        else if (text.startsWith(" "))
          rows.push({
            text: text.slice(1),
            kind: "context",
            old: old++,
            next: next++,
          });
      }
    });
  return rows;
}
