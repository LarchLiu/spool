import { useEffect, useRef } from "react";
import { Plus } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import type { Translator } from "./i18n";
import {
  formatUnreadCount,
  isPeopleGroup,
  type PeopleChatGroup,
} from "./direct-messages.js";
import { UserAvatar } from "./user-avatar.js";
import { PeopleGroupAvatar } from "./people-group-avatar.js";

export function PeopleConversationList({
  value,
  people,
  groups = [],
  resolvePerson = (id) =>
    people.find((person) => person.id === id) || { id, nickname: id },
  presence = {},
  connected = true,
  unread,
  t,
  onSelect,
  onNewGroup,
}: {
  value: string;
  people: AvatarIdentity[];
  groups?: PeopleChatGroup[];
  resolvePerson?: (id: string) => AvatarIdentity;
  presence?: Record<string, string>;
  connected?: boolean;
  unread: Record<string, number>;
  t: Translator;
  onSelect: (id: string) => void;
  onNewGroup?: () => void;
}) {
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => {
    const option = list.current?.querySelector<HTMLElement>(
      '[aria-selected="true"]',
    );
    if (!option || !list.current) return;
    const viewport = list.current.getBoundingClientRect();
    const bounds = option.getBoundingClientRect();
    if (bounds.top < viewport.top)
      list.current.scrollTop += bounds.top - viewport.top;
    else if (bounds.bottom > viewport.bottom)
      list.current.scrollTop += bounds.bottom - viewport.bottom;
  }, [value]);

  return (
    <aside className="direct-message-sidebar" aria-label={t("People & groups")}>
      <div className="direct-message-sidebar-heading">
        <h3>{t("People & groups")}</h3>
        {onNewGroup && (
          <button
            type="button"
            className="icon"
            aria-label={t("New group chat")}
            title={t("New group chat")}
            onClick={onNewGroup}
          >
            <Plus size={15} aria-hidden="true" />
          </button>
        )}
      </div>
      <ul
        ref={list}
        className="direct-message-conversations"
        role="listbox"
        aria-label={t("Choose conversation")}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          const options = [
            ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
              '[role="option"]',
            ),
          ];
          const index = options.indexOf(
            document.activeElement as HTMLButtonElement,
          );
          const next =
            event.key === "ArrowDown"
              ? (index + 1) % options.length
              : event.key === "ArrowUp"
                ? (index - 1 + options.length) % options.length
                : event.key === "Home"
                  ? 0
                  : event.key === "End"
                    ? options.length - 1
                    : -1;
          if (next >= 0) {
            event.preventDefault();
            event.stopPropagation();
            options[next]?.focus();
          }
        }}
      >
        {people.map((person) => {
          const isGroup = isPeopleGroup(person.id);
          const state = !connected
            ? "disconnected"
            : presence[person.id] === "online"
              ? "online"
              : presence[person.id] === "away"
                ? "away"
                : "offline";
          const status = t(
            state === "online"
              ? "Online"
              : state === "away"
                ? "Away"
                : state === "offline"
                  ? "Offline"
                  : "Reconnecting…",
          );
          return (
            <li key={person.id} role="none">
              <button
                type="button"
                role="option"
                aria-label={
                  isGroup ? person.nickname : `${person.nickname} (${status})`
                }
                aria-selected={person.id === value}
                tabIndex={person.id === value ? 0 : -1}
                data-conversation={person.id}
                title={person.nickname}
                onClick={() => onSelect(person.id)}
              >
                {isGroup ? (
                  <PeopleGroupAvatar
                    people={groups
                      .find((group) => group.id === person.id)
                      ?.members.map(resolvePerson)}
                  />
                ) : (
                  <span className="direct-message-person-avatar">
                    <UserAvatar person={person} size={30} />
                    <span
                      className="direct-message-presence"
                      data-state={state}
                      title={status}
                      aria-hidden="true"
                    />
                  </span>
                )}
                <span className="direct-message-conversation-name">
                  {person.nickname}
                </span>
                {!!unread[person.id] && (
                  <small className="direct-unread">
                    {formatUnreadCount(unread[person.id])}
                  </small>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </aside>
  );
}
