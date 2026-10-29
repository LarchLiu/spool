import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import type { MessageKey, Translator } from "./i18n";
import { DirectMessageError, type PeopleChatGroup } from "./direct-messages.js";
import { UserAvatar } from "./user-avatar.js";

export function PeopleGroupPicker({
  people,
  group,
  t,
  onSubmit,
  onClose,
  onDone,
}: {
  people: AvatarIdentity[];
  group?: PeopleChatGroup;
  t: Translator;
  onSubmit: (name: string, members: string[]) => Promise<PeopleChatGroup>;
  onClose: () => void;
  onDone: (group: PeopleChatGroup) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const mounted = useRef(true);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | "">("");
  const limit = 32 - (group?.members.length || 1);
  useEffect(() => {
    mounted.current = true;
    dialog.current?.showModal();
    return () => {
      mounted.current = false;
    };
  }, []);
  const available = people.map((person) => person.id).join(",");
  useEffect(() => {
    const ids = new Set(available.split(","));
    setSelected((old) => old.filter((id) => ids.has(id)));
  }, [available]);
  const submit = async () => {
    if (busy || !selected.length) return;
    setBusy(true);
    setError("");
    try {
      const result = await onSubmit(name, selected);
      if (mounted.current) onDone(result);
    } catch (error) {
      if (!mounted.current) return;
      const code = error instanceof DirectMessageError ? error.code : "unknown";
      const errors: Record<string, MessageKey> = {
        offline:
          "Some selected people went offline. Update your selection and try again.",
        invalid_group:
          "Choose 1-31 other people. Groups support up to 32 members.",
        forbidden: "This group is no longer available to you.",
        group_limit:
          "Too many active groups. Try again after unused groups close.",
        unavailable: "Chat disconnected. Wait for reconnection before sending.",
        unknown:
          "Group operation status unknown. Check your conversation list before trying again.",
        rate_limited: "You are sending too quickly. Try again shortly.",
      };
      setError(errors[code] || errors.unknown);
    } finally {
      if (mounted.current) setBusy(false);
    }
  };
  return (
    <dialog
      ref={dialog}
      className="people-group-picker"
      aria-labelledby="people-group-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="direct-message-heading">
        <h2 id="people-group-title">
          {t(group ? "Invite people" : "New group chat")}
        </h2>
        <button
          className="icon"
          aria-label={t("Close group chat")}
          onClick={onClose}
        >
          <X size={18} />
        </button>
      </div>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        {!group && (
          <label className="people-group-name">
            {t("Group name")}
            <input
              autoFocus
              value={name}
              maxLength={80}
              disabled={busy}
              placeholder={t("Group chat")}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
        )}
        <p className="direct-message-notice">
          {t(
            "Only selected members can see this group. No history is saved; the group closes when everyone disconnects.",
          )}
        </p>
        <fieldset className="people-group-members" disabled={busy}>
          <legend>{t("Select online people")}</legend>
          {!people.length && <p>{t("No other people online.")}</p>}
          {people.map((person) => (
            <label key={person.id}>
              <input
                type="checkbox"
                value={person.id}
                checked={selected.includes(person.id)}
                disabled={
                  !selected.includes(person.id) && selected.length >= limit
                }
                onChange={(event) =>
                  setSelected((old) =>
                    event.target.checked
                      ? [...old, person.id]
                      : old.filter((id) => id !== person.id),
                  )
                }
              />
              <UserAvatar person={person} size={26} />
              <span>{person.nickname}</span>
            </label>
          ))}
        </fieldset>
        {error && (
          <p className="direct-message-status error" role="alert">
            {t(error)}
          </p>
        )}
        <div className="people-group-actions">
          <small>{t("{count} selected", { count: selected.length })}</small>
          <button
            className="primary"
            type="submit"
            disabled={busy || !selected.length}
          >
            {t(busy ? "Working…" : group ? "Invite people" : "Create group")}
          </button>
        </div>
      </form>
    </dialog>
  );
}
