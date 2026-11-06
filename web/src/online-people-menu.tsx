import { useEffect, useRef } from "react";
import { MessageCircle, Plus, Users } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import {
  formatUnreadCount,
  onlineChatRoom,
  type PeopleChatGroup,
} from "./direct-messages.js";
import type { Translator } from "./i18n";
import { PeopleGroupAvatar } from "./people-group-avatar.js";
import { UserAvatar } from "./user-avatar.js";

/** Shared header presence menu, including the mobile inbox entry. */
export function OnlinePeopleMenu({
  self,
  people,
  groups,
  unread,
  unreadMessages,
  resolvePerson,
  t,
  onInbox,
  onPeer,
  onNewGroup,
}: {
  self: string;
  people: (AvatarIdentity & { state: string })[];
  groups: PeopleChatGroup[];
  unread: Record<string, number>;
  unreadMessages: number;
  resolvePerson: (id: string) => AvatarIdentity;
  t: Translator;
  onInbox: () => void;
  onPeer: (id: string) => void;
  onNewGroup: () => void;
}) {
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const disclosure = menu.current?.closest("details");
    if (!disclosure) return;
    const outside = (event: PointerEvent) => {
      if (disclosure.open && !disclosure.contains(event.target as Node))
        disclosure.open = false;
    };
    const escape = (event: KeyboardEvent) => {
      if (!disclosure.open || event.key !== "Escape" || event.isComposing)
        return;
      event.preventDefault();
      event.stopPropagation();
      disclosure.open = false;
      disclosure.querySelector("summary")?.focus({ preventScroll: true });
    };
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", escape);
    };
  }, []);
  return (
    <div className="online-list" ref={menu}>
      <div className="online-inbox-row">
        <button
          className="online-chat-person online-inbox-button"
          onClick={(event) => {
            onInbox();
            event.currentTarget.closest("details")?.removeAttribute("open");
          }}
        >
          {t("People chat")}
          {!!unreadMessages && (
            <span className="direct-unread">
              {formatUnreadCount(unreadMessages)}
            </span>
          )}
        </button>
      </div>
      <div className="online-group-row">
        <button
          className="online-chat-person"
          onClick={(event) => {
            onPeer(onlineChatRoom);
            event.currentTarget.closest("details")?.removeAttribute("open");
          }}
        >
          <Users size={16} aria-hidden="true" />
          {t("Online group chat")}
          {!!unread[onlineChatRoom] && (
            <span className="direct-unread">
              {formatUnreadCount(unread[onlineChatRoom])}
            </span>
          )}
        </button>
        <button
          className="quiet online-new-group"
          onClick={(event) => {
            onNewGroup();
            event.currentTarget.closest("details")?.removeAttribute("open");
          }}
        >
          <Plus size={14} />
          {t("New group chat")}
        </button>
      </div>
      {groups.map((group) => (
        <div className="online-group-row" key={group.id}>
          <button
            className="online-chat-person"
            data-conversation={group.id}
            onClick={(event) => {
              onPeer(group.id);
              event.currentTarget.closest("details")?.removeAttribute("open");
            }}
          >
            <PeopleGroupAvatar people={group.members.map(resolvePerson)} />
            {resolvePerson(group.id).nickname}
            {!!unread[group.id] && (
              <span className="direct-unread">
                {formatUnreadCount(unread[group.id])}
              </span>
            )}
          </button>
        </div>
      ))}
      {people.map((u) => (
        <div key={u.id}>
          <UserAvatar person={u} size={26} />
          <span className={u.state === "online" ? "online-dot" : "away-dot"} />
          {u.id === self ? (
            <span>{u.nickname}</span>
          ) : (
            <button
              className="online-chat-person"
              onClick={(event) => {
                onPeer(u.id);
                event.currentTarget.closest("details")?.removeAttribute("open");
              }}
              title={t("Chat with {name}", { name: u.nickname })}
            >
              {u.nickname}
              {!!unread[u.id] && (
                <span className="direct-unread">
                  {formatUnreadCount(unread[u.id])}
                </span>
              )}
              <MessageCircle size={13} aria-hidden="true" />
            </button>
          )}
          <small>{u.state === "online" ? t("Online") : t("Away")}</small>
        </div>
      ))}
      {!people.length && <small>{t("Updating presence…")}</small>}
    </div>
  );
}
