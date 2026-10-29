import { useEffect, useState } from "react";
import { Check, Copy, CircleAlert } from "lucide-react";
import type { Translator } from "./i18n.js";
import type { Locale } from "./preferences.js";

export async function copyMessageText(
  text: string,
  clipboard: Pick<Clipboard, "writeText"> | undefined = navigator.clipboard,
  doc: Document = document,
) {
  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(text);
      return;
    } catch {
      // Plain HTTP deployments and embedded webviews may need the legacy API.
    }
  }
  const focused = doc.activeElement as HTMLElement | null;
  const selection = doc.getSelection();
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) =>
        selection.getRangeAt(index).cloneRange(),
      )
    : [];
  const textarea = doc.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.cssText =
    "position:fixed;top:0;left:0;opacity:0;pointer-events:none;font-size:16px";
  doc.body.append(textarea);
  try {
    textarea.focus({ preventScroll: true });
    textarea.select();
    if (!doc.execCommand("copy")) throw new Error("Clipboard copy failed");
  } finally {
    textarea.remove();
    focused?.focus({ preventScroll: true });
    if (selection) {
      selection.removeAllRanges();
      for (const range of ranges) selection.addRange(range);
    }
  }
}

export function messageTime(timestamp: number | undefined, locale: Locale) {
  if (!timestamp || timestamp < 0 || !Number.isFinite(timestamp)) return null;
  const date = new Date(timestamp);
  if (!Number.isFinite(date.getTime())) return null;
  const now = new Date();
  const sameYear = date.getFullYear() === now.getFullYear();
  const isToday =
    sameYear &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate();
  return {
    dateTime: date.toISOString(),
    label: date.toLocaleString(locale, {
      year: sameYear ? undefined : "numeric",
      month: isToday ? undefined : "2-digit",
      day: isToday ? undefined : "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    }),
    title: date.toLocaleString(locale, { hour12: false }),
  };
}

export function MessageFooter({
  role,
  text,
  timestamp,
  locale = "en",
  t,
}: {
  role: "user" | "assistant";
  text: string;
  timestamp?: number;
  locale?: Locale;
  t: Translator;
}) {
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  useEffect(() => {
    if (copyState === "idle") return;
    const timeout = setTimeout(() => setCopyState("idle"), 2000);
    return () => clearTimeout(timeout);
  }, [copyState]);
  const time = messageTime(timestamp, locale);
  const timeLabel = time && (
    <time dateTime={time.dateTime} title={time.title}>
      {time.label}
    </time>
  );
  const copyLabel = t(
    copyState === "copied"
      ? "Message copied"
      : copyState === "failed"
        ? "Could not copy message"
        : "Copy message",
  );
  const Icon =
    copyState === "copied"
      ? Check
      : copyState === "failed"
        ? CircleAlert
        : Copy;
  return (
    <div className={`message-footer message-footer-${role}`}>
      {role === "user" && timeLabel}
      <button
        type="button"
        className="message-copy"
        aria-label={copyLabel}
        title={copyLabel}
        disabled={!text}
        onClick={async () => {
          try {
            await copyMessageText(text);
            setCopyState("copied");
          } catch {
            setCopyState("failed");
          }
        }}
      >
        <Icon size={13} aria-hidden="true" />
      </button>
      {role === "assistant" && timeLabel}
      <span className="message-copy-status" role="status">
        {copyState !== "idle" ? copyLabel : ""}
      </span>
    </div>
  );
}
