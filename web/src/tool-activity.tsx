import { imageSource } from "./image-resource";
import { ResourceImage } from "./resource-image";
import { Component, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ChevronDown,
  ChevronRight,
  FilePenLine,
  FilePlus2,
  FileX2,
  Globe,
  List,
  Search,
  Terminal,
  Wrench,
  Brain,
  BookOpen,
  Images,
  ImageOff,
  MessageCircle,
  Bot,
} from "lucide-react";
import { FileDiff } from "@pierre/diffs/react";
import type { Backend, TaskStep, TranscriptPart } from "./types";
import type { Translator } from "./i18n";
import { MarkdownMessage, MarkdownPreview } from "./markdown-message.js";
import { patchText, toolResultDiffs } from "./tool-diff.js";
import { turnPresentation } from "./turn-presentation.js";
import { MessageFooter } from "./message-footer.js";
import type { Locale } from "./preferences.js";
import { firstToolDetailCursor, mergeToolDetailPage, requestToolDetail, toolDetailCache, type ToolDetailCursor, type ToolDetailPage } from "./tool-detail.js";
import { LocalFileContext } from "./local-file-context.js";
import { UserMessage } from "./user-message.js";
import { localFileTarget } from "./local-file-links.js";
import { toolNameKind, toolCommand, shellCommandKind, shellOperations, shellTarget, type ActivityKind } from "./tool-kind.js";

export { toolResultDiffs } from "./tool-diff.js";

type Activity = {
  use?: TranscriptPart;
  result?: TranscriptPart;
  kind: ActivityKind;
  label: string;
  detail: string;
  stats?: string;
  images?: string[];
  imageURLs?: string[];
  noDetails?: boolean;
  input?: TranscriptPart[];
};

// What the provider reported about a call. Its use and its result can each
// carry a status; a failure on either one is the call's outcome.
export function activityOutcome(activity: Pick<Activity, "use" | "result">) {
  const use = activity.use?.tool, result = activity.result?.tool;
  const failed = [use?.status, result?.status].find(status => status === "failed" || status === "declined");
  return { status: failed || result?.status || use?.status, exitCode: use?.exit_code ?? result?.exit_code };
}

function outcomeLabel(activity: Pick<Activity, "use" | "result">, t: Translator) {
  const { status, exitCode } = activityOutcome(activity);
  if (activity.use?.tool?.kind === "task") {
    if (status === "running") return t("Running");
    if (status === "completed") return t("Completed");
    if (status === "stopped") return t("Stopped");
    if (status === "unknown") return t("Status unavailable");
  }
  if (status === "declined") return t("Declined");
  if (status !== "failed") return "";
  return exitCode ? t("Exit code {code}", { code: exitCode }) : t("Failed");
}

type ActivityGroup = {
  kind: ActivityKind;
  label: string;
  activities: Activity[];
  compound?: boolean;
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

const imageToolName = /(?:^|\.|__)(?:view_?image|image_?view)$/i;

export function viewedImagePaths(part: TranscriptPart): string[] {
  if (part.tool) return part.tool.image_paths || [];
  const name = part.name?.trim().toLowerCase() || "";
  if (!imageToolName.test(name) && name !== "multi_tool_use.parallel" && name !== "read") return [];
  return imageCallPaths(name, payload(part.text));
}

function imageCallPaths(name: string, data: Record<string, unknown> | undefined): string[] {
  if (!data) return [];
  if (imageToolName.test(name) || name === "read") {
    for (const key of ["path", "file_path", "image_path"]) {
      const path = data[key];
      if (typeof path === "string" && path.trim() && path.length <= 4096 && !/[\0\r\n]/.test(path)) {
        if (name === "read" && !/\.(?:png|jpe?g|gif|webp)$/i.test(path)) return [];
        return [path];
      }
    }
    return [];
  }
  const calls = data.tool_uses;
  if (name !== "multi_tool_use.parallel" || !Array.isArray(calls) || !calls.length || calls.length > 64) return [];
  const paths: string[] = [];
  for (const call of calls) {
    const tool = typeof call?.recipient_name === "string" ? call.recipient_name.trim().toLowerCase() : "";
    if (!imageToolName.test(tool)) return [];
    const images = imageCallPaths(tool, call.parameters);
    if (!images.length) return [];
    paths.push(...images);
  }
  return paths;
}

function patchFiles(part: TranscriptPart) {
  const name = part.name?.trim().toLowerCase() || "";
  if (!/(?:^|\.|__)(?:apply_?patch|patch|file_?change)$/.test(name)) return [];
  return Array.from(
    part.text.matchAll(/^\*\*\* (Add|Update|Delete) File: (.+)\r?$/gm),
    ([, action, path]) => ({ action, path: path.trim() }),
  );
}

function classify(part: TranscriptPart, data: Record<string, unknown> | undefined): ActivityKind {
  if (part.kind === "thinking") return "thinking";
  const kind = toolNameKind(part.name || "");
  if (/(?:^|\.|__)(apply_?patch|patch|file_?change)$/i.test(part.name?.trim() || "")) {
    const files = patchFiles(part);
    if (files.length && files.every(({ action }) => action === "Add")) return "create";
    if (files.length && files.every(({ action }) => action === "Delete")) return "delete";
    return "write";
  }
  return kind === "shell" ? shellCommandKind(toolCommand(part.text, data)) : kind;
}

function shellSummary(part: TranscriptPart, data: Record<string, unknown> | undefined) {
  if (toolNameKind(part.name || "") !== "shell") return undefined;
  const command = toolCommand(part.text, data);
  return /^(read|list|search)$/.test(shellCommandKind(command)) ? shellTarget(command) : undefined;
}

function extractQuery(part: TranscriptPart, data: Record<string, unknown> | undefined) {
  return shellSummary(part, data)?.query || stringField(data, [
    "pattern",
    "query",
    "search_query",
    "searchTerm",
    "glob",
    "include",
  ]);
}

function extractPath(part: TranscriptPart, data: Record<string, unknown> | undefined) {
  const target = shellSummary(part, data)?.path;
  if (target) return target;
  const direct = stringField(data, pathKeys);
  if (direct) return direct;
  const files = patchFiles(part);
  if (files.length === 1) return files[0].path;
  if (files.length > 1) return "";
  if (toolNameKind(part.name || "") === "shell") return "";
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

export function displayActivity(
  part: TranscriptPart,
  backend: Backend | undefined,
  t: Translator,
): Omit<Activity, "result"> {
  const data = part.tool ? undefined : payload(part.text);
  const images = viewedImagePaths(part);
  const kind = images.length ? "image" : part.tool?.kind || classify(part, data);
  const name = part.name?.trim() || (backend === "claude" ? "Claude tool" : "Codex tool");
  const path = part.tool?.path || extractPath(part, data);
  const query = part.tool?.query || extractQuery(part, data);
  let label = "";
  switch (kind) {
    case "task":
      label = name;
      break;
    case "image":
      label = images.length === 1 ? t("Viewed 1 image") : t("Viewed {count} images", { count: images.length });
      break;
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
        : path ? `${t("Searched files")} · ${shortPath(path)}` : t("Searched files");
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
  return { use: part, kind, label, detail: part.text, images, imageURLs: part.tool?.image_urls, stats: part.tool?.stats || inlineChangeStats(part.text) };
}

export function activitiesFromParts(
  parts: TranscriptPart[],
  backend: Backend | undefined,
  t: Translator,
): Activity[] {
  const out: Activity[] = [];
  const calls = new Map<string, Activity>();
  for (const [index, part] of parts.entries()) {
    if (part.role === "user") {
      const last = out[out.length - 1];
      const inputID = last?.input?.reduce((id, input) => input.id || id, "");
      const inputTime = last?.input?.find(input => input.timestamp)?.timestamp;
      if (parts[index - 1]?.role === "user" && last?.kind === "user" && (!inputID || !part.id || inputID === part.id) && (!inputTime || !part.timestamp || inputTime === part.timestamp)) last.input!.push(part);
      else out.push({ kind: "user", label: "", detail: "", use: part, input: [part] });
      continue;
    }
    if (part.role === "assistant" && part.kind === "text") {
      if (part.text.trim())
        out.push({ use: part, kind: "message", label: "", detail: part.text });
      continue;
    }
    if (part.kind === "tool_result") {
      const last = out[out.length - 1];
      const call = part.id ? calls.get(part.id) : undefined;
      const activity = call || (!part.id && last?.use?.kind === "tool_use" && !last.result ? last : undefined);
      const imageURLs = part.tool?.image_urls || part.images || [];
      if (activity) {
        activity.result = part;
        if (imageURLs.length) {
          activity.kind = "image";
          activity.imageURLs = imageURLs;
          activity.label = imageURLs.length === 1 ? t("Viewed 1 image") : t("Viewed {count} images", { count: imageURLs.length });
        }
      } else if (imageURLs.length) {
        out.push({ kind: "image", label: imageURLs.length === 1 ? t("Viewed 1 image") : t("Viewed {count} images", { count: imageURLs.length }), detail: "", result: part, imageURLs });
      } else out.push({ kind: "other", label: t("Tool result"), detail: "", result: part });
      continue;
    }
    if (
      part.kind !== "tool_use" &&
      part.kind !== "thinking"
    )
      continue;
    const activity = displayActivity(part, backend, t);
    out.push(activity);
    if (part.id && part.kind === "tool_use") calls.set(part.id, activity);
  }
  // Pair provider call/result IDs first. Derived rows share that provenance,
  // without implying that the combined output belongs to an individual step.
  return out.flatMap((activity) => {
    const part = activity.use;
    if (part?.kind !== "tool_use" || activity.kind === "image") return [activity];
    const operations = part.tool ? part.tool.operations
      : toolNameKind(part.name || "") === "shell" ? shellOperations(toolCommand(part.text, payload(part.text))) : undefined;
    if (!operations?.length) {
      if (["failed", "declined", "running"].includes(activityOutcome(activity).status || "")) {
        const path = part.tool?.path || extractPath(part, payload(part.text));
        const labels = {
          write: path ? t("Edit {path}", { path: shortPath(path) }) : t("Edit files"),
          create: path ? t("Create {path}", { path: shortPath(path) }) : t("Create files"),
          delete: path ? t("Delete {path}", { path: shortPath(path) }) : t("Delete files"),
        };
        if (activity.kind in labels) activity.label = labels[activity.kind as keyof typeof labels];
      }
      return [activity];
    }
    return operations.map((operation) => {
      const display = displayActivity({ ...part, text: "", tool: { kind: operation.kind, path: operation.path, query: operation.query, version: "", bytes: 0 } }, backend, t);
      return { ...display, use: part, result: activity.result, detail: "", stats: "", noDetails: true,
        label: operation.path || operation.query ? display.label : `${display.label} · ${oneLine(operation.command)}` };
    });
  });
}

function groupLabel(group: Activity[], t: Translator) {
  if (group.length === 1) return group[0].label;
  if (group.every(activity => ["failed", "declined", "running"].includes(activityOutcome(activity).status || ""))) {
    if (group[0].kind === "write") return t("Edit files");
    if (group[0].kind === "create") return t("Create files");
    if (group[0].kind === "delete") return t("Delete files");
  }
  if (group[0].kind === "image") return t("Viewed {count} images", { count: group.reduce((count, item) => count + (item.imageURLs?.length || item.images?.length || 0), 0) });
  return categoryLabel(group[0].kind, t);
}

function categoryLabel(kind: ActivityKind, t: Translator) {
  switch (kind) {
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

function compoundLabel(activities: Activity[], t: Translator) {
  const kinds = [...new Set(activities.map(activity => activity.kind))];
  return kinds.map(kind => {
    const items = activities.filter(activity => activity.kind === kind);
    if (kind === "shell" && items.length === 1) return t("Ran command");
    return categoryLabel(kind, t);
  }).reduce((previous, next) => t("{previous}, {next}", {
    previous, next: next.charAt(0).toLowerCase() + next.slice(1),
  }));
}

function groupActivities(activities: Activity[], t: Translator): ActivityGroup[] {
  const groups: ActivityGroup[] = [];
  for (const activity of activities) {
    const last = groups[groups.length - 1];
    if (activity.noDetails) {
      if (last?.compound && last.activities[0].use === activity.use) {
        last.activities.push(activity);
        last.label = compoundLabel(last.activities, t);
      } else groups.push({ kind: activity.kind, label: compoundLabel([activity], t), activities: [activity], compound: true });
      continue;
    }
    if (last && !last.compound && last.kind === activity.kind && activity.kind !== "message" && activity.kind !== "user" && activity.kind !== "task") {
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
    case "task":
      return Bot;
    case "image":
      return Images;
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
      return Globe;
    case "shell":
      return Terminal;
    case "thinking":
      return Brain;
    default:
      return Wrench;
  }
}

function ViewedImage({ path = "", source, t }: { path?: string; source?: string; t: Translator }) {
  const { imageURL, previewImage, openFile } = useContext(LocalFileContext);
  const target = localFileTarget(path);
  const url = (source && imageSource(source)) || (target && imageURL?.(target)) || undefined;
  const name = path.replaceAll("\\", "/").split("/").pop() || "image";
  const [failedURL, setFailedURL] = useState<string>();
  const available = url && failedURL !== url;
  return (
    <button
      type="button"
      className="viewed-image"
      title={path}
      aria-label={available ? t("Preview {name}", { name }) : t("Image unavailable: {name}", { name })}
      disabled={!available && (!target || !openFile)}
      onClick={() => {
        if (available && previewImage) previewImage(name, url);
        else if (target) openFile?.(target);
      }}
    >
      {available ? (
        <ResourceImage key={url} url={url} name={name} t={t} loading="lazy" compact onFailure={() => setFailedURL(url)} />
      ) : (
        <span className="viewed-image-unavailable"><ImageOff size={20} /><span>{name}</span></span>
      )}
    </button>
  );
}

function ViewedImages({ group, t, open, onToggle }: { group: ActivityGroup; t: Translator; open: boolean; onToggle: () => void }) {
  const images = group.activities.flatMap<{ source?: string; path?: string }>((item) => item.imageURLs?.length
    ? item.imageURLs.map((source, index) => ({ source, path: item.images?.[index] }))
    : (item.images || []).map((path) => ({ path, source: undefined })));
  return (
    <div className="viewed-images">
      <button type="button" className="tool-activity-summary" aria-expanded={open} onClick={onToggle}>
        <Images size={13} strokeWidth={1.6} />
        <span>{group.label}</span>
        <span className="tool-activity-caret">{open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span>
      </button>
      {open && <div className="viewed-image-thumbnails">{images.map(({ path, source }, index) => <ViewedImage key={`${index}:${path || source}`} path={path} source={source} t={t} />)}</div>}
    </div>
  );
}

export class ToolDiffBoundary extends Component<
  { raw: string; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? <pre>{this.props.raw}</pre> : this.props.children;
  }
}

// The header metadata slot adds to the built-in counters. Hide those counters
// inside the diff's shadow root so all tool stats use the same baseline layout.
const patchOptions = { unsafeCSS: "[data-metadata] > [data-additions-count], [data-metadata] > [data-deletions-count] { display: none; }" } as const;
const contextPatchOptions = { ...patchOptions, disableLineNumbers: true, hunkSeparators: "simple" } as const;

function patchHeaderStats(file: ReturnType<typeof toolResultDiffs>[number]) {
  const additions = file.hunks.reduce((sum, hunk) => sum + hunk.additionLines, 0);
  const deletions = file.hunks.reduce((sum, hunk) => sum + hunk.deletionLines, 0);
  return <ActivityChangeStats stats={`+${additions} -${deletions}`} />;
}

function ToolPatch({ text, t, input = false }: { text: string; t: Translator; input?: boolean }) {
  const files = useMemo(() => toolResultDiffs(text), [text]);
  if (!files.length) return <pre>{text}</pre>;
  return (
    <ToolDiffBoundary key={text} raw={text}>
      {files.map((file, index) => (
        <div className="tool-diff" key={`${index}:${file.name}`}>
          <FileDiff
            fileDiff={file}
            options={file.lineNumbersUnavailable ? contextPatchOptions : patchOptions}
            renderHeaderMetadata={patchHeaderStats}
            disableWorkerPool
          />
          {file.contentsUnavailable && <p className="tool-diff-note">{t("File contents are not included in this patch.")}</p>}
        </div>
      ))}
      <details className="tool-result-source">
        <summary>{t(input ? "Raw input" : "Raw output")}</summary>
        <pre>{text}</pre>
      </details>
    </ToolDiffBoundary>
  );
}

export function changeStats(activities: Activity[]) {
  if (!activities.every(({ kind }) => ["write", "create", "delete"].includes(kind)))
    return "";
  let additions = 0;
  let deletions = 0;
  for (const activity of activities) {
    // A change that failed or was declined did not happen.
    if (/^(failed|declined)$/.test(activityOutcome(activity).status || "")) continue;
    const changes = activity.result?.tool?.changes || activity.use?.tool?.changes;
    if (changes) {
      additions += changes.additions;
      deletions += changes.deletions;
      continue;
    }
    const value = patchText(activity.result?.text || "") || patchText(activity.use?.text || "");
    if (value) {
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

function ActivityChangeStats({ stats }: { stats: string }) {
  const [additions, deletions] = stats.split(" ");
  return <span className="tool-activity-stats">
    <span className="tool-activity-additions">{additions}</span>{" "}
    <span className="tool-activity-deletions">−{deletions.slice(1)}</span>
  </span>;
}

function inlineChangeStats(value: string) {
  const match = value.match(/(?:^|\s)(\+\d+)\s+(-\d+)\s*$/);
  return match ? `${match[1]} ${match[2]}` : "";
}

function LazyToolBody({ part, t, thinking, input }: { part: TranscriptPart; t: Translator; thinking: boolean; input: boolean }) {
  const tool = part.tool!;
  const [cursor, setCursor] = useState<ToolDetailCursor>(firstToolDetailCursor);
  const [page, setPage] = useState<ToolDetailPage>();
  const current = useRef<ToolDetailPage>(undefined);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [retry, setRetry] = useState(0);
  const latestVersion = useRef(tool.version);
  latestVersion.current = tool.version;
  const refresh = useRef<() => void>(() => {});
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let busy = false;
    let completedVersion = "";
    const load = () => {
      if (busy || controller.signal.aborted || completedVersion === latestVersion.current) return;
      busy = true;
      const version = latestVersion.current;
      setLoading(true); setError("");
      const previous = current.current;
      const from = previous && previous.next === previous.total && previous.next - previous.offset < 32 * 1024
        ? { offset: previous.next, prefix: previous.prefix } : cursor;
      void toolDetailCache.load(requestToolDetail, tool.href!, version, from, controller.signal)
        .then((incoming) => {
          if (controller.signal.aborted) return;
          const merged = mergeToolDetailPage(current.current, incoming);
          current.current = merged; setPage(merged);
          completedVersion = version;
          setLoading(false);
        })
        .catch((reason: unknown) => {
          if (!controller.signal.aborted) { setLoading(false); setError(String(reason instanceof Error ? reason.message : reason)); }
        })
        .finally(() => {
          busy = false;
          if (!controller.signal.aborted && version !== latestVersion.current) schedule();
        });
    };
    // Throttle rather than debounce: continuous output must not keep cancelling
    // the request and prevent a Think/tool body from ever appearing.
    const schedule = () => {
      if (timer !== undefined || controller.signal.aborted) return;
      timer = setTimeout(() => { timer = undefined; load(); }, 100);
    };
    refresh.current = schedule;
    schedule();
    return () => { clearTimeout(timer); controller.abort(); refresh.current = () => {}; };
  }, [tool.href, cursor, retry]);
  useEffect(() => { refresh.current(); }, [tool.version]);
  const move = (next: ToolDetailCursor) => { current.current = undefined; setPage(undefined); setCursor(next); };
  return <>
    {page && (page.offset === 0 && page.next === page.total
      ? thinking ? <MarkdownMessage t={t}>{page.text}</MarkdownMessage> : <ToolPatch text={page.text} t={t} input={input} />
      : <pre>{page.text}</pre>)}
    {loading && !page && <p role="status">{t("Loading tool details…")}</p>}
    {error && <p role="alert">{error} <button type="button" onClick={() => setRetry((value) => value + 1)}>{t("Retry")}</button></p>}
    {page && (page.offset > 0 || page.next < page.total) && <div className="tool-detail-pages">
      <span>{t("Showing bytes {start}–{end} of {total}", { start: page.offset, end: page.next, total: page.total })}</span>
      {page.offset > 0 && <button type="button" onClick={() => move(firstToolDetailCursor)}>{t("Back to start")}</button>}
      {page.next < page.total && <button type="button" onClick={() => move({ offset: page.next, prefix: page.prefix })}>{t("Load next section")}</button>}
    </div>}
  </>;
}

export function ActivityDetail({ activity, t }: { activity: Activity; t: Translator }) {
  if (activity.noDetails) return null;
  if (activity.kind === "task") return activity.use?.tool?.task ? <TaskDetail activity={activity} t={t} /> : <div className="tool-detail"><pre>{activity.use?.text}</pre></div>;
  const command = activity.use?.text || "";
  const result = activity.result?.text || "";
  return (
    <div className="tool-detail">
      {(command?.trim() || activity.use?.tool?.href) && (
        <div className="tool-detail-block">
          <span className="tool-detail-label">
            {activity.kind === "thinking" ? t("Thought") : t("Command")}
            {activity.kind !== "thinking" && activity.use?.name && ` · ${activity.use.name}`}
          </span>
          {activity.use?.tool?.href ? <LazyToolBody key={activity.use.tool.ref} part={activity.use} t={t} thinking={activity.kind === "thinking"} input /> : activity.kind === "thinking" ? (
            <MarkdownMessage t={t}>{command}</MarkdownMessage>
          ) : (
            <ToolPatch text={command} t={t} input />
          )}
        </div>
      )}
      {(result || activity.result?.tool?.href) && (
        <div className="tool-detail-block">
          <span className="tool-detail-label">{t("Result")}</span>
          {activity.result?.tool?.href ? <LazyToolBody key={activity.result.tool.ref} part={activity.result} t={t} thinking={false} input={false} /> : <ToolPatch text={result!} t={t} />}
        </div>
      )}
    </div>
  );
}

// How long something took, or has taken so far.
export function durationLabel(ms: number, t: Translator) {
  const seconds = Math.max(0, Math.round(ms / 1000));
  if (seconds < 60) return t("{seconds}s", { seconds });
  if (seconds < 3600) return t("{minutes}m {seconds}s", { minutes: Math.floor(seconds / 60), seconds: seconds % 60 });
  return t("{hours}h {minutes}m", { hours: Math.floor(seconds / 3600), minutes: Math.floor((seconds % 3600) / 60) });
}

function compactCount(count: number) {
  if (count < 1000) return String(count);
  const [value, unit] = count < 1_000_000 ? [count / 1000, "k"] : [count / 1_000_000, "M"];
  return value.toFixed(1).replace(/\.0$/, "") + unit;
}

// A child's step is named as the same call is in the main activity list.
export function taskStepLabel(step: TaskStep, t: Translator) {
  if (step.kind === "message") return oneLine(step.text || "", 240);
  const { label } = displayActivity({ role: "assistant", kind: "tool_use", name: step.name, text: "",
    tool: { kind: step.kind, path: step.path, query: step.query, version: "", bytes: 0 } }, undefined, t);
  return step.text ? `${label} · ${oneLine(step.text)}` : label;
}

// A running child is timed from when it was first seen, by a clock of its own:
// its row gets no event while it works on one long step. A finished child took
// as long as its provider, or the server, says it did.
function useTaskFacts(activity: Activity, t: Translator) {
  const tool = activity.use?.tool, task = tool?.task;
  const running = activityOutcome(activity).status === "running";
  const startedAt = running ? task?.started_at : undefined;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!startedAt) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [startedAt]);
  const duration = startedAt ? Math.max(0, now - startedAt) : tool?.duration_ms || 0;
  const calls = task?.tool_uses || 0;
  return {
    running,
    calls: calls === 1 ? t("1 tool use") : calls ? t("{count} tool uses", { count: calls }) : "",
    tokens: task?.tokens ? t("{count} tokens", { count: compactCount(task.tokens) }) : "",
    duration: duration ? durationLabel(duration, t) : "",
  };
}

function TaskLabel({ activity, t }: { activity: Activity; t: Translator }) {
  const task = activity.use!.tool!.task!;
  const facts = useTaskFacts(activity, t);
  // What a working child is doing now is its latest call, by name if no more is known.
  const call = facts.running ? task.steps?.filter(step => step.kind !== "message").at(-1) : undefined;
  const doing = call ? taskStepLabel(call, t) : facts.running ? task.last_tool : "";
  const meta = [task.agent_type, doing, facts.calls, facts.duration].filter(Boolean);
  return <span className="tool-task-label">
    <span className="tool-task-title">{activity.label}</span>
    {task.summary && <span className="tool-task-progress">{oneLine(task.summary, 240)}</span>}
    {meta.length > 0 && <span className="tool-task-progress tool-task-meta">{meta.join(" · ")}</span>}
  </span>;
}

function TaskDetail({ activity, t }: { activity: Activity; t: Translator }) {
  const task = activity.use!.tool!.task!;
  const facts = useTaskFacts(activity, t);
  const status = activityOutcome(activity).status;
  const steps = task.steps || [];
  const meta = [task.agent_type, task.background && t("Background"), facts.calls, facts.tokens, facts.duration].filter(Boolean);
  if (!task.prompt && !steps.length && !task.summary && !meta.length) return <div className="tool-detail"><pre>{activity.use?.text}</pre></div>;
  return <div className="tool-detail tool-task-detail">
    {task.prompt && <div className="tool-detail-block">
      <span className="tool-detail-label">{t("Prompt")}</span>
      <pre>{task.prompt}</pre>
    </div>}
    {steps.length > 0 && <div className="tool-detail-block">
      <span className="tool-detail-label">{t("Recent steps")}</span>
      <ol className="tool-task-steps">
        {steps.map((step, index) => {
          const Icon = step.kind === "message" ? MessageCircle : iconFor(step.kind);
          const outcome = step.status === "running" ? t("Running") : step.status === "failed" ? t("Failed") : step.status === "declined" ? t("Declined") : "";
          return <li key={index} className={`tool-task-step tool-task-step-${step.kind === "message" ? "message" : "call"}${step.status === "running" ? " tool-task-step-running" : ""}`}>
            <Icon size={12} strokeWidth={1.6} />
            <span className="tool-task-step-label">{taskStepLabel(step, t)}</span>
            {outcome && <span className="tool-activity-outcome">{outcome}</span>}
          </li>;
        })}
      </ol>
    </div>}
    {task.summary && <div className="tool-detail-block">
      <span className="tool-detail-label">{status === "completed" || status === "failed" ? t("Result") : t("Latest update")}</span>
      <MarkdownMessage t={t}>{task.summary}</MarkdownMessage>
    </div>}
    {meta.length > 0 && <div className="tool-detail-block tool-task-facts">{meta.join(" · ")}</div>}
  </div>;
}

function ActivityLabel({ activity, t }: { activity: Activity; t: Translator }) {
  if (activity.kind === "task" && activity.use?.tool?.task) return <TaskLabel activity={activity} t={t} />;
  if (activity.kind !== "thinking") return <span>{activity.label}</span>;
  const preview = activity.use?.tool?.preview || activity.use?.text;
  // Render the prefix separately so Markdown at the beginning of a thought
  // (for example, a heading or a list) is still parsed before it is clipped.
  return (
    <span className="tool-thinking-label">
      <span className="tool-thinking-label-prefix">{activity.label}</span>
      {preview?.trim() && (
        <>
          <span className="tool-thinking-label-separator" aria-hidden="true">
            {" "}·{" "}
          </span>
          <span className="tool-thinking-label-preview">
            <MarkdownPreview>{preview}</MarkdownPreview>
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
  outcome: reportedOutcome,
}: {
  activity: Activity;
  open: boolean;
  onToggle: () => void;
  t: Translator;
  outcome?: string;
}) {
  const Icon = iconFor(activity.kind);
  const stats = changeStats([activity]);
  const outcome = activity.noDetails ? "" : reportedOutcome ?? outcomeLabel(activity, t);
  if (activity.noDetails) return (
    <div className="tool-activity-item">
      <div className="tool-activity-item-summary tool-activity-static" title={activity.label}>
        <Icon size={12} strokeWidth={1.6} />
        <ActivityLabel activity={activity} t={t} />
      </div>
    </div>
  );
  return (
    <div className={"tool-activity-item " + (activity.kind === "task" ? `tool-task tool-task-${activityOutcome(activity).status || "unknown"} ` : "") + (open ? "expanded" : "")}>
      <button
        type="button"
        className="tool-activity-item-summary"
        title={activity.use?.name}
        aria-expanded={open}
        onClick={onToggle}
      >
        <Icon size={12} strokeWidth={1.6} />
        <ActivityLabel activity={activity} t={t} />
        {activity.kind === "task" && activity.use?.tool?.task?.background && <span className="tool-task-badge">{t("Background")}</span>}
        {outcome && <span className="tool-activity-outcome">{outcome}</span>}
        {stats && <ActivityChangeStats stats={stats} />}
        <span className="tool-activity-caret">
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
      </button>
      {open && <ActivityDetail activity={activity} t={t} />}
    </div>
  );
}

// Remote calls use the same expandable task row as native child agents. Its
// data is supplied by the task API, independently of the model's tool context.
export function TaskActivity({ part, t, outcome }: { part: TranscriptPart; t: Translator; outcome?: string }) {
  const [open, setOpen] = useState(false);
  return <ActivityItem activity={displayActivity(part, undefined, t)} open={open} onToggle={() => setOpen(value => !value)} t={t} outcome={outcome} />;
}

// The rows of a compound command describe its steps; they share one execution.
// The complete command and its combined output stay available under the rows.
function CompoundActivities({ group, t, open, onToggle }: { group: ActivityGroup; t: Translator; open: boolean; onToggle: () => void }) {
  const Icon = iconFor(group.kind);
  const [detailOpen, setDetailOpen] = useState(false);
  const whole: Activity = { ...group.activities[0], kind: "shell", label: t("Command and output"), noDetails: false };
  const outcome = outcomeLabel(whole, t);
  return <div className={`tool-activity tool-activity-compound${open ? " expanded" : ""}`}>
    <button type="button" className="tool-activity-summary" aria-expanded={open} onClick={onToggle}>
      <Icon size={13} strokeWidth={1.6} />
      <span>{group.label}</span>
      {outcome && <span className="tool-activity-outcome">{outcome}</span>}
      <span className="tool-activity-caret">{open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span>
    </button>
    {open && <div className="tool-activity-items">
      {group.activities.map((activity, index) =>
        <ActivityItem key={index} activity={activity} open={false} onToggle={() => {}} t={t} />)}
      <div className={"tool-activity-item tool-activity-whole " + (detailOpen ? "expanded" : "")}>
        <button type="button" className="tool-activity-item-summary" aria-expanded={detailOpen} onClick={() => setDetailOpen(value => !value)}>
          <Terminal size={12} strokeWidth={1.6} />
          <span>{whole.label}</span>
          <span className="tool-activity-caret">{detailOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}</span>
        </button>
        {detailOpen && <ActivityDetail activity={whole} t={t} />}
      </div>
    </div>}
  </div>;
}

export function ToolActivityList({
  parts,
  backend,
  t,
  live = false,
  summary,
  renderUser,
}: {
  parts: TranscriptPart[];
  backend?: Backend;
  t: Translator;
  live?: boolean;
  summary?: ReactNode;
  renderUser?: (parts: TranscriptPart[]) => ReactNode;
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
  const [openCompounds, setOpenCompounds] = useState<Set<string>>(new Set());
  const [openImages, setOpenImages] = useState<Map<number, boolean>>(new Map());
  const [activityOpen, setActivityOpen] = useState(live);
  const groupCount = groups.length;
  useEffect(() => {
    setActivityOpen(live);
  }, [live]);
  useEffect(() => {
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
            if (group.kind === "user") {
              const input = group.activities[0].input!;
              return <div className="tool-progress-user" key={`user:${groupIndex}:${input[0].id || ""}`}>
                {renderUser ? renderUser(input) : <UserMessage text={input.filter(part => part.kind === "text").map(part => part.text).join("\n")} t={t} />}
              </div>;
            }
            if (group.compound) {
              const key = `compound:${groupIndex}:${group.activities[0].use?.id || group.activities[0].use?.tool?.ref || ""}`;
              return <CompoundActivities key={key} group={group} t={t} open={openCompounds.has(key)} onToggle={() => setOpenCompounds(old => {
                const next = new Set(old);
                if (!next.delete(key)) next.add(key);
                return next;
              })} />;
            }
            if (group.kind === "image") return <ViewedImages key={`images:${groupIndex}`} group={group} t={t}
              open={openImages.get(groupIndex) ?? live} onToggle={() => setOpenImages(old => {
                const next = new Map(old);
                next.set(groupIndex, !(old.get(groupIndex) ?? live));
                return next;
              })} />;
            if (group.kind === "message") {
              return (
                <div className="tool-progress-message" key={`message:${group.activities[0].use?.id || ""}:${groupIndex}`}>
                  <MarkdownMessage t={t}>{group.activities[0].detail}</MarkdownMessage>
                </div>
              );
            }
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
            const failures = group.activities.filter(activity => /^(failed|declined)$/.test(activityOutcome(activity).status || "")).length;
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
                  {failures > 0 && <span className="tool-activity-outcome">{t("{count} failed", { count: failures })}</span>}
                  {stats && <ActivityChangeStats stats={stats} />}
                  <span className="tool-activity-caret">
                    {expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                  </span>
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

export function TurnResponse({
  parts,
  backend,
  t,
  running,
  summary,
  timestamp,
  locale,
  actions,
  renderUser,
}: {
  parts: TranscriptPart[];
  backend?: Backend;
  t: Translator;
  running: boolean;
  summary: ReactNode;
  timestamp?: number;
  locale?: Locale;
  actions?: ReactNode;
  renderUser?: (parts: TranscriptPart[]) => ReactNode;
}) {
  const { process, answer, expanded } = turnPresentation(parts, running);
  // A final main reply can precede background-child completion. Keep live
  // tasks visible while the ordinary process folds as it always has.
  const tasks = running ? process.filter(part => part.tool?.kind === "task") : [];
  const taskSet = new Set(tasks);
  return (
    <>
      <ToolActivityList parts={process.filter(part => !taskSet.has(part))} backend={backend} t={t} live={expanded} summary={summary} renderUser={renderUser} />
      {tasks.length > 0 && <ToolActivityList parts={tasks} backend={backend} t={t} live />}
      {answer && (
        <div className="assistant">
          <div className="assistant-text">
            <MarkdownMessage t={t}>{answer}</MarkdownMessage>
          </div>
          {actions}
          <MessageFooter
            role="assistant"
            text={answer}
            timestamp={timestamp}
            locale={locale}
            t={t}
          />
        </div>
      )}
      {!answer && actions}
    </>
  );
}

