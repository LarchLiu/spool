import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { createPortal } from "react-dom";
import type { AvatarIdentity } from "./avatar.js";
import { AvatarStack, UserAvatar } from "./user-avatar.js";
import { turnOperator } from "./chat-state.js";
import { messageTime } from "./message-footer.js";
import type { Chat, Turn } from "./types.js";
import type { Translator } from "./i18n.js";
import type { Locale } from "./preferences.js";

export function ChatOperatorAvatars({
  chat,
  resolve,
}: {
  chat: Chat;
  resolve: (id: string) => AvatarIdentity;
}) {
  const people = useMemo(
    () => chat.participants?.map((person) => resolve(person.id)) || [],
    [chat.participants, resolve],
  );
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const hovered = useRef(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ left: number; top: number }>();

  function cancelClose() {
    clearTimeout(closeTimer.current);
  }
  function show() {
    cancelClose();
    setOpen(true);
  }
  function hide() {
    cancelClose();
    setOpen(false);
  }
  function leave() {
    cancelClose();
    // Leave enough time to cross the gap between the stack and its portal.
    closeTimer.current = setTimeout(() => {
      if (!hovered.current && document.activeElement !== trigger.current)
        setOpen(false);
    }, 150);
  }
  function enter(event: ReactPointerEvent) {
    if (event.pointerType === "touch") return;
    hovered.current = true;
    show();
  }
  function exit(event: ReactPointerEvent) {
    if (event.pointerType === "touch") return;
    hovered.current = false;
    leave();
  }

  useEffect(() => () => cancelClose(), []);
  useLayoutEffect(() => {
    if (!open || !trigger.current || !popup.current) return;
    const anchor = trigger.current.getBoundingClientRect();
    const panel = popup.current.getBoundingClientRect();
    const edge = 8;
    const above = anchor.top - panel.height - 6;
    const top = above >= edge ? above : anchor.bottom + 6;
    setPosition({
      left: Math.max(
        edge,
        Math.min(anchor.left, window.innerWidth - panel.width - edge),
      ),
      top: Math.max(
        edge,
        Math.min(top, window.innerHeight - panel.height - edge),
      ),
    });
  }, [open, people]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !popup.current?.contains(event.target as Node)
      )
        hide();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        hide();
      }
    };
    const scroll = (event: Event) => {
      if (!popup.current?.contains(event.target as Node)) hide();
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", hide);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", hide);
    };
  }, [open]);

  if (!people.length) return null;
  const names = people.map((person) => person.nickname).join(", ");
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="chat-operators"
        aria-label={names}
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-describedby={open ? id : undefined}
        onPointerEnter={enter}
        onPointerLeave={exit}
        onFocus={show}
        onBlur={leave}
        onClick={show}
      >
        <AvatarStack people={people} showTitles={false} />
      </button>
      {open &&
        createPortal(
          <div
            ref={popup}
            id={id}
            role="tooltip"
            className="chat-operator-popover"
            style={{ ...position, visibility: position ? "visible" : "hidden" }}
            onPointerEnter={enter}
            onPointerLeave={exit}
          >
            <ChatOperatorList people={people} />
          </div>,
          document.body,
        )}
    </>
  );
}

export function ChatOperatorList({ people }: { people: AvatarIdentity[] }) {
  return (
    <ul className="chat-operator-list">
      {people.map((person) => (
        <li key={person.id}>
          <UserAvatar person={person} size={28} />
          <span>{person.nickname}</span>
        </li>
      ))}
    </ul>
  );
}

export function TurnOperatorAvatar({ person }: { person?: AvatarIdentity }) {
  if (!person) return null;
  return (
    <span
      className="turn-operator-avatar"
      role="img"
      aria-label={person.nickname}
      title={person.nickname}
    >
      <UserAvatar person={person} size={28} />
    </span>
  );
}

export function TurnActions({
  chat,
  turn,
  locale,
  t,
  resolve,
}: {
  chat: Chat;
  turn: Turn;
  locale: Locale;
  t: Translator;
  resolve?: (id: string) => AvatarIdentity;
}) {
  if (!turn.actions?.length) return null;
  return (
    <ul className="turn-actions">
      {turn.actions.map((action) => {
        const person = turnOperator(
          chat,
          { ...turn, user_id: action.user_id },
          null,
          resolve,
        );
        const time = messageTime(action.created_at, locale);
        const label =
          action.kind === "stop"
            ? t("Stopped by {name}", {
                name: person?.nickname || action.user_id,
              })
            : action.kind === "approve"
              ? t("Approved by {name}", {
                  name: person?.nickname || action.user_id,
                })
              : t("Denied by {name}", {
                  name: person?.nickname || action.user_id,
                });
        return (
          <li key={action.id}>
            {person && <UserAvatar person={person} size={18} />}
            <span>{label}</span>
            {time && (
              <time dateTime={time.dateTime} title={time.title}>
                {time.label}
              </time>
            )}
          </li>
        );
      })}
    </ul>
  );
}
