import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  ChevronDown,
  ChevronRight,
  FilePenLine,
  FilePlus2,
  FileX2,
  Globe2,
  List,
  Search,
  Terminal,
  Wrench,
  Brain,
  BookOpen,
} from "lucide-react";
import { PatchDiff } from "@pierre/diffs/react";
import type { Backend, TranscriptPart } from "./types";
import type { Translator } from "./i18n";
import { MarkdownMessage } from "./markdown-message";

type ActivityKind =
  | "read"
  | "write"
  | "create"
  | "delete"
  | "search"
  | "list"
  | "web"
  | "shell"
  | "thinking"
  | "other";

type Activity = {
  use?: TranscriptPart;
  result?: TranscriptPart;
  kind: ActivityKind;
  label: string;
  detail: string;
  stats?: string;
};

type ActivityGroup = {
  kind: ActivityKind;
  label: string;
  activities: Activity[];
};

const pathKeys = [
  "file_path",
  "path",
  "file",
  "filename",
  "target_file",
  "notebook_path",
];

function payload(text: string): Record<string, unknown> | undefined {
  const value = text.trim();
  if (!value.startsWith("{") || !value.endsWith("}")) return undefined;
  try {
    const parsed: unknown = JSON.parse(value);
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, unknown>)
      : undefined;
  } catch {
    return undefined;
  }
}

function stringField(input: Record<string, unknown> | undefined, keys: string[]) {
  if (!input) return "";
  for (const key of keys) {
    const value = input[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function classify(part: TranscriptPart): ActivityKind {
  if (part.kind === "thinking") return "thinking";
  if (/^(reason|reasoning|think|thinking)$/i.test(part.name?.trim() || ""))
    return "thinking";
  const name = `${part.name || ""} ${part.text}`.toLowerCase();
  if (/(websearch|web.?fetch|browser|internet|search the web|lookup url)/.test(name))
    return "web";
  if (/(glob|list.?files|list files|directory listing|readdir|ls\b|find\b)/.test(name))
    return "list";
  if (/(grep|ripgrep|rg\b|search files|search code|find in files|regex)/.test(name))
    return "search";
  if (/(\bread\b|cat\b|head\b|tail\b|get.?file|open.?file|view file)/.test(name))
    return "read";
  if (
    part.name?.trim().toLowerCase() === "write" ||
    /(create|created|new file|add file|add_file)/.test(name)
  )
    return "create";
  if (/(delete|deleted|remove|removed|unlink|delete_file)/.test(name))
    return "delete";
  if (/(write|edit|filechange|apply.?patch|patch|replace|create.?file|delete.?file)/.test(name))
    return "write";
  if (/(shell|bash|zsh|command|terminal|exec|run command)/.test(name))
    return "shell";
  return "other";
}

function extractQuery(part: TranscriptPart, data: Record<string, unknown> | undefined) {
  return stringField(data, [
    "pattern",
    "query",
    "search_query",
    "searchTerm",
    "glob",
    "include",
  ]);
}

function extractPath(part: TranscriptPart, data: Record<string, unknown> | undefined) {
  const direct = stringField(data, pathKeys);
  if (direct) return direct;
  const match = part.text.match(/(?:^|\s)([\w./~-]+\.[\w-]+)(?:\s|$)/);
  return match?.[1] || "";
}

function shortPath(path: string) {
  const pieces = path.replaceAll("\\", "/").split("/").filter(Boolean);
  return pieces.length > 3 ? `…/${pieces.slice(-3).join("/")}` : path;
}

function oneLine(value: string, limit = 180) {
  const text = value.replace(/\s+/g, " ").trim();
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

function titleName(value: string) {
  const name = value.replace(/[_-]+/g, " ").trim();
  return name ? name.charAt(0).toUpperCase() + name.slice(1) : name;
}

function displayActivity(
  part: TranscriptPart,
  backend: Backend | undefined,
  t: Translator,
): Omit<Activity, "result"> {
  const data = payload(part.text);
  const kind = classify(part);
  const name = part.name?.trim() || (backend === "claude" ? "Claude tool" : "Codex tool");
  const path = extractPath(part, data);
  const query = extractQuery(part, data);
  let label = "";
  switch (kind) {
    case "read":
      label = path ? t("Read {path}", { path: shortPath(path) }) : t("Read files");
      break;
    case "write":
      label = path
        ? t("Edited {path}", { path: shortPath(path) })
        : t("Edited files");
      break;
    case "create":
      label = path
        ? t("Created {path}", { path: shortPath(path) })
        : t("Created files");
      break;
    case "delete":
      label = path
        ? t("Deleted {path}", { path: shortPath(path) })
        : t("Deleted files");
      break;
    case "search":
      label = query
        ? path
          ? t("Searched for {query} in {path}", {
              query: oneLine(query, 80),
              path: shortPath(path),
            })
          : t("Searched for {query}", { query: oneLine(query, 80) })
        : t("Searched files");
      break;
    case "list":
      label = path
        ? t("Listed files in {path}", { path: shortPath(path) })
        : t("Listed files");
      break;
    case "web":
      label = query
        ? t("Searched web for {query}", { query: oneLine(query, 80) })
        : t("Searched the web");
      break;
    case "shell":
      label = t("Ran command");
      break;
    case "thinking":
      // Keep the complete Markdown source here. Truncating the source before
      // parsing can cut through markers (for example, an unmatched `**`) and
      // make the compact preview render as plain text. The preview is clipped
      // after Markdown has been rendered by the styles below.
      label = t("Think");
      break;
    default:
      label = name ? t("Used {tool}", { tool: titleName(name) }) : t("Used tool");
      break;
  }
  return { use: part, kind, label, detail: part.text, stats: inlineChangeStats(part.text) };
}

function activitiesFromParts(
  parts: TranscriptPart[],
  backend: Backend | undefined,
  t: Translator,
): Activity[] {
  const out: Activity[] = [];
  for (const part of parts) {
    if (part.role === "user") continue;
    if (part.kind === "tool_result") {
      const last = out[out.length - 1];
      if (last && !last.result) last.result = part;
      else out.push({ kind: "other", label: t("Tool result"), detail: "", result: part });
      continue;
    }
    if (
      part.kind !== "tool_use" &&
      part.kind !== "thinking"
    )
      continue;
    out.push(displayActivity(part, backend, t));
  }
  return out;
}

function groupLabel(group: Activity[], t: Translator) {
  if (group.length === 1) return group[0].label;
  switch (group[0].kind) {
    case "read":
      return t("Read files");
    case "write":
      return t("Edited files");
    case "create":
      return t("Created files");
    case "delete":
      return t("Deleted files");
    case "search":
      return t("Searched files");
    case "list":
      return t("Listed files");
    case "web":
      return t("Searched the web");
    case "shell":
      return t("Ran commands");
    case "thinking":
      return t("Thought through tasks");
    default:
      return t("Used tools");
  }
}

function groupActivities(activities: Activity[], t: Translator): ActivityGroup[] {
  const groups: ActivityGroup[] = [];
  for (const activity of activities) {
    const last = groups[groups.length - 1];
    if (last && last.kind === activity.kind) {
      last.activities.push(activity);
      last.label = groupLabel(last.activities, t);
      continue;
    }
    groups.push({
      kind: activity.kind,
      label: activity.label,
      activities: [activity],
    });
  }
  return groups;
}

function iconFor(kind: ActivityKind) {
  switch (kind) {
    case "read":
      return BookOpen;
    case "write":
      return FilePenLine;
    case "create":
      return FilePlus2;
    case "delete":
      return FileX2;
    case "search":
      return Search;
    case "list":
      return List;
    case "web":
      return Globe2;
    case "shell":
      return Terminal;
    case "thinking":
      return Brain;
    default:
      return Wrench;
  }
}

function patchText(value: string) {
  const start = value.search(/(?:^|\n)(?:diff --git |--- [^\n]+\n\+\+\+ |\*\*\* Begin Patch)/);
  if (start < 0) return "";
  const patch = value.slice(start).replace(/^\n/, "").trim();
  return patch.includes("@@") || patch.includes("*** Begin Patch") ? patch : "";
}

function changeStats(activities: Activity[]) {
  if (!activities.every(({ kind }) => ["write", "create", "delete"].includes(kind)))
    return "";
  let additions = 0;
  let deletions = 0;
  for (const activity of activities) {
    const value = activity.result?.text;
    if (value && patchText(value)) {
      for (const line of value.split(/\r?\n/)) {
        if (line.startsWith("+++") || line.startsWith("---")) continue;
        if (line.startsWith("+")) additions++;
        else if (line.startsWith("-")) deletions++;
      }
    } else if (activity.stats) {
      const match = activity.stats.match(/^\+(\d+)\s+-(\d+)$/);
      if (match) {
        additions += Number(match[1]);
        deletions += Number(match[2]);
      }
    }
  }
  return additions || deletions ? `+${additions} -${deletions}` : "";
}

function inlineChangeStats(value: string) {
  const match = value.match(/(?:^|\s)(\+\d+)\s+(-\d+)\s*$/);
  return match ? `${match[1]} ${match[2]}` : "";
}

function ActivityDetail({ activity, t }: { activity: Activity; t: Translator }) {
  const command = activity.use?.text.trim();
  const result = activity.result?.text.trim();
  const diff = result ? patchText(result) : "";
  return (
    <div className="tool-detail">
      {command && (
        <div className="tool-detail-block">
          <span className="tool-detail-label">
            {activity.kind === "thinking" ? t("Thought") : t("Command")}
          </span>
          {activity.kind === "thinking" ? (
            <MarkdownMessage>{command}</MarkdownMessage>
          ) : (
            <pre>{command}</pre>
          )}
        </div>
      )}
      {result && (
        <div className="tool-detail-block">
          <span className="tool-detail-label">{t("Result")}</span>
          {diff ? (
            <div className="tool-diff">
              <PatchDiff patch={diff} disableWorkerPool />
            </div>
          ) : (
            <pre>{result}</pre>
          )}
        </div>
      )}
    </div>
  );
}

function ActivityLabel({ activity }: { activity: Activity }) {
  if (activity.kind !== "thinking") return <span>{activity.label}</span>;
  // Render the prefix separately so Markdown at the beginning of a thought
  // (for example, a heading or a list) is still parsed before it is clipped.
  return (
    <span className="tool-thinking-label">
      <span className="tool-thinking-label-prefix">{activity.label}</span>
      {activity.use?.text.trim() && (
        <>
          <span className="tool-thinking-label-separator" aria-hidden="true">
            {" "}·{" "}
          </span>
          <span className="tool-thinking-label-preview">
            <MarkdownMessage>{activity.use.text}</MarkdownMessage>
          </span>
        </>
      )}
    </span>
  );
}

function ActivityItem({
  activity,
  open,
  onToggle,
  t,
}: {
  activity: Activity;
  open: boolean;
  onToggle: () => void;
  t: Translator;
}) {
  const Icon = iconFor(activity.kind);
  const stats = changeStats([activity]);
  return (
    <div className={"tool-activity-item " + (open ? "expanded" : "")}>
      <button
        type="button"
        className="tool-activity-item-summary"
        aria-expanded={open}
        onClick={onToggle}
      >
        <Icon size={12} strokeWidth={1.6} />
        <ActivityLabel activity={activity} />
        <span className="tool-activity-caret">
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
        {stats && <span className="tool-activity-stats">{stats}</span>}
      </button>
      {open && <ActivityDetail activity={activity} t={t} />}
    </div>
  );
}

export function ToolActivityList({
  parts,
  backend,
  t,
  live = false,
  summary,
}: {
  parts: TranscriptPart[];
  backend?: Backend;
  t: Translator;
  live?: boolean;
  summary?: ReactNode;
}) {
  const activities = useMemo(
    () => activitiesFromParts(parts, backend, t),
    [parts, backend, t],
  );
  const groups = useMemo(() => groupActivities(activities, t), [activities, t]);
  const [openGroups, setOpenGroups] = useState<Set<number>>(() =>
    live ? new Set(groups.map((_, index) => index)) : new Set(),
  );
  const [openItems, setOpenItems] = useState<Set<string>>(new Set());
  const [activityOpen, setActivityOpen] = useState(live);
  const groupCount = groups.length;
  useEffect(() => {
    setActivityOpen(live);
    setOpenGroups(
      live
        ? new Set(Array.from({ length: groupCount }, (_, index) => index))
        : new Set(),
    );
  }, [groupCount, live]);
  if (!groups.length) {
    return summary ? <div className="work-label">{summary}</div> : null;
  }
  return (
    <div className="tool-activity-history">
      {summary && (
        <button
          type="button"
          className="work-label work-label-toggle"
          aria-expanded={activityOpen}
          onClick={() => setActivityOpen((open) => !open)}
        >
          <span className="work-label-caret" aria-hidden="true">
            {activityOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </span>
          <span>{summary}</span>
        </button>
      )}
      {activityOpen && (
        <div className="tool-activity-list">
          {groups.map((group, groupIndex) => {
            if (group.activities.length === 1) {
              const itemID = `${groupIndex}:0`;
              return (
                <ActivityItem
                  key={itemID}
                  activity={group.activities[0]}
                  open={openItems.has(itemID)}
                  onToggle={() =>
                    setOpenItems((old) => {
                      const next = new Set(old);
                      if (next.has(itemID)) next.delete(itemID);
                      else next.add(itemID);
                      return next;
                    })
                  }
                  t={t}
                />
              );
            }
            const expanded = openGroups.has(groupIndex);
            const Icon = iconFor(group.kind);
            const stats = changeStats(group.activities);
            return (
              <div className={"tool-activity " + (expanded ? "expanded" : "")} key={groupIndex}>
                <button
                  type="button"
                  className="tool-activity-summary"
                  aria-expanded={expanded}
                  onClick={() =>
                    setOpenGroups((old) => {
                      const next = new Set(old);
                      if (next.has(groupIndex)) next.delete(groupIndex);
                      else next.add(groupIndex);
                      return next;
                    })
                  }
                >
                  <Icon size={13} strokeWidth={1.6} />
                  <span>{group.label}</span>
                  <span className="tool-activity-caret">
                    {expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                  </span>
                  {stats && <span className="tool-activity-stats">{stats}</span>}
                </button>
                {expanded && (
                  <div className="tool-activity-items">
                    {group.activities.map((activity, itemIndex) => {
                      const itemID = `${groupIndex}:${itemIndex}`;
                      return (
                        <ActivityItem
                          key={itemID}
                          activity={activity}
                          open={openItems.has(itemID)}
                          onToggle={() =>
                            setOpenItems((old) => {
                              const next = new Set(old);
                              if (next.has(itemID)) next.delete(itemID);
                              else next.add(itemID);
                              return next;
                            })
                          }
                          t={t}
                        />
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

