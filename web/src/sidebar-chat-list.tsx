import type { ReactNode } from "react";
import type { Translator } from "./i18n";
import type { Locale } from "./preferences";
import type { Chat } from "./types";
import { messageTime } from "./message-footer";
import {
  INITIAL_CHAT_LIMIT,
  nextSidebarChatLimit,
  sidebarChatAge,
} from "./sidebar-state.js";
export { INITIAL_CHAT_LIMIT } from "./sidebar-state.js";

export function SidebarChatTime({
  timestamp,
  now,
  locale,
}: {
  timestamp: number;
  now: number;
  locale: Locale;
}) {
  const age = sidebarChatAge(timestamp, now);
  const time = messageTime(timestamp, locale);
  if (!age || !time) return null;
  return (
    <time
      className="nav-chat-time"
      dateTime={time.dateTime}
      title={time.title}
      aria-label={time.title}
    >
      {age}
    </time>
  );
}

type Props = {
  chats: Chat[];
  limit: number;
  nested?: boolean;
  t: Translator;
  onLimit: (limit: number) => void;
  renderChat: (chat: Chat) => ReactNode;
};

export function SidebarChatList({
  chats,
  limit,
  nested = false,
  t,
  onLimit,
  renderChat,
}: Props) {
  const hasMore = chats.length > limit;
  return (
    <>
      {chats.slice(0, limit).map(renderChat)}
      {!chats.length && (
        <p className={"nav-empty" + (nested ? " nested" : "")}>
          {t("No conversations yet.")}
        </p>
      )}
      {chats.length > INITIAL_CHAT_LIMIT && (
        <button
          type="button"
          className={"show-more" + (nested ? " nested" : "")}
          onClick={() => onLimit(nextSidebarChatLimit(chats.length, limit))}
        >
          {t(hasMore ? "Show more" : "Show less")}
        </button>
      )}
    </>
  );
}
