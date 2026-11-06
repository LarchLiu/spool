import { useEffect, useState } from "react";
import { Check, Copy, CircleAlert } from "lucide-react";
import type { Translator } from "./i18n.js";
import type { Locale } from "./preferences.js";
import { copyMessageText } from "./copy-text.js";
export { copyMessageText } from "./copy-text.js";

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
  author,
  text,
  timestamp,
  locale = "en",
  t,
}: {
  role: "user" | "assistant";
  author?: string;
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
      {role === "user" && author && (
        <span className="message-author">{author}</span>
      )}
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
