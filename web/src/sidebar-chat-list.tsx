import type { ReactNode } from "react";
import type { Translator } from "./i18n";
import type { Chat } from "./types";

export const INITIAL_CHAT_LIMIT = 5;
const CHAT_PAGE_SIZE = 10;

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
          onClick={() =>
            onLimit(
              hasMore
                ? Math.min(limit + CHAT_PAGE_SIZE, chats.length)
                : INITIAL_CHAT_LIMIT,
            )
          }
        >
          {t(hasMore ? "Show more" : "Show less")}
        </button>
      )}
    </>
  );
}
