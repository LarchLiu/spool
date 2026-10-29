import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import type { Translator } from "./i18n";

export function PeopleConversationPicker({
  value,
  people,
  unread,
  height,
  t,
  onSelect,
}: {
  value: string;
  people: AvatarIdentity[];
  unread: Record<string, number>;
  height: number;
  t: Translator;
  onSelect: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const id = useId();
  const selected = people.find((person) => person.id === value);

  useLayoutEffect(() => {
    if (open) {
      const option = list.current?.querySelector<HTMLElement>(
        '[aria-selected="true"]',
      );
      option?.focus({ preventScroll: true });
      option?.scrollIntoView({ block: "nearest" });
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [open]);

  return (
    <div
      className="direct-message-people"
      ref={root}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (open && event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          trigger.current?.focus({ preventScroll: true });
        }
      }}
    >
      <button
        ref={trigger}
        type="button"
        className="direct-message-conversation"
        aria-label={t("Choose conversation")}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <span>{selected?.nickname || value}</span>
        {!!unread[value] && (
          <small className="direct-unread">{unread[value]}</small>
        )}
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <ul
          ref={list}
          id={id}
          className="direct-message-conversations"
          role="listbox"
          aria-label={t("Choose conversation")}
          style={{ maxHeight: Math.min(220, Math.max(40, height - 160)) }}
          onKeyDown={(event) => {
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
              options[next]?.focus();
            }
          }}
        >
          {people.map((person) => (
            <li key={person.id} role="none">
              <button
                type="button"
                role="option"
                aria-selected={person.id === value}
                tabIndex={person.id === value ? 0 : -1}
                data-conversation={person.id}
                onClick={() => {
                  setOpen(false);
                  trigger.current?.focus({ preventScroll: true });
                  onSelect(person.id);
                }}
              >
                <span>{person.nickname}</span>
                {!!unread[person.id] && (
                  <small className="direct-unread">{unread[person.id]}</small>
                )}
                {person.id === value && <Check size={14} aria-hidden="true" />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
