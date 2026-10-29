import { useEffect, useRef, useState } from "react";
import { ArrowUp, LoaderCircle, UserPlus, Users, X } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import type { Translator, MessageKey } from "./i18n";
import type { DirectMessage } from "./direct-messages.js";
import {
  DirectMessageError,
  directMessageLimit,
  isPeopleGroup,
  onlineChatRoom,
  type PeopleChatGroup,
} from "./direct-messages.js";
import { UserAvatar } from "./user-avatar.js";
import { AutosizeTextarea } from "./autosize-textarea.js";
import { useFloatingWindow } from "./use-floating-window.js";
import { PeopleConversationPicker } from "./people-conversation-picker.js";
import {
  floatingWindowEdges,
  type FloatingWindowBounds,
} from "./floating-window.js";

export function DirectMessagePanel({
  user,
  peer,
  people,
  unread,
  messages,
  connected,
  status,
  onlineCount = 0,
  group,
  locale,
  t,
  onSend,
  onPeer,
  onClose,
  onInvite,
  windowBounds,
  onWindowBoundsChange,
}: {
  user: string;
  peer: AvatarIdentity;
  people: AvatarIdentity[];
  unread: Record<string, number>;
  messages: DirectMessage[];
  connected: boolean;
  status?: string;
  onlineCount?: number;
  group?: PeopleChatGroup;
  locale: string;
  t: Translator;
  onSend: (to: string, text: string) => Promise<DirectMessage>;
  onPeer: (id: string) => void;
  onClose: () => void;
  onInvite?: () => void;
  windowBounds?: FloatingWindowBounds | null;
  onWindowBoundsChange?: (bounds: FloatingWindowBounds) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const log = useRef<HTMLDivElement>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<MessageKey | "">("");
  const mounted = useRef(true);
  const floating = useFloatingWindow(windowBounds, onWindowBoundsChange);
  const moveLabel = t("Move chat window (arrow keys)");
  const resizeLabel = t("Resize chat window (arrow keys)");
  useEffect(() => {
    mounted.current = true;
    dialog.current?.show();
    dialog.current?.querySelector("textarea")?.focus({ preventScroll: true });
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [messages.at(-1)?.id]);
  useEffect(() => {
    const active = document.activeElement;
    if (
      !sending &&
      (active === document.body ||
        active === dialog.current?.querySelector('button[type="submit"]'))
    )
      dialog.current?.querySelector("textarea")?.focus();
  }, [sending]);
  const isGroup = isPeopleGroup(peer.id);
  const isRoom = peer.id === onlineChatRoom;
  const groupAvailable = isRoom || !!group;
  const writable =
    connected && (isGroup ? groupAvailable : !!status) && !sending;
  const send = async () => {
    if (!writable || !draft.trim()) return;
    setSending(true);
    setError("");
    try {
      await onSend(peer.id, draft);
      if (mounted.current) setDraft("");
    } catch (error) {
      if (!mounted.current) return;
      const code = error instanceof DirectMessageError ? error.code : "unknown";
      const errors: Record<string, MessageKey> = {
        offline: "This person is offline. Messages are not queued.",
        unavailable: "Chat disconnected. Wait for reconnection before sending.",
        invalid_message: "Enter a message of up to 2000 characters.",
        rate_limited: "You are sending too quickly. Try again shortly.",
        unknown:
          "Send status unknown. The message may have arrived; it will not be retried automatically.",
        forbidden: "This group is no longer available to you.",
      };
      setError(errors[code] || errors.unknown);
    } finally {
      if (mounted.current) setSending(false);
    }
  };
  return (
    <dialog
      ref={dialog}
      className="direct-message-panel"
      aria-labelledby="direct-message-title"
      aria-modal="false"
      style={{
        left: floating.bounds.x,
        top: floating.bounds.y,
        width: floating.bounds.width,
        height: floating.bounds.height,
      }}
      data-operation={floating.operation || undefined}
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.nativeEvent.isComposing) {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="direct-message-heading">
        <div
          className="direct-message-drag"
          role="button"
          tabIndex={0}
          aria-label={moveLabel}
          title={moveLabel}
          {...floating.moveHandle}
        >
          {isGroup ? (
            <span className="group-chat-avatar">
              <Users size={22} />
            </span>
          ) : (
            <UserAvatar person={peer} size={34} />
          )}
          <div>
            <h2 id="direct-message-title" title={peer.nickname}>
              {isGroup
                ? peer.nickname
                : t("Chat with {name}", { name: peer.nickname })}
            </h2>
            <small role="status">
              {!connected
                ? t("Reconnecting…")
                : isRoom
                  ? t("{count} online", { count: onlineCount })
                  : isGroup
                    ? t("{count} members", {
                        count: group?.members.length || 0,
                      })
                    : status
                      ? t(status === "online" ? "Online" : "Away")
                      : t("Offline")}
            </small>
          </div>
        </div>
        {group && (
          <button
            className="icon"
            aria-label={t("Invite people")}
            title={t("Invite people")}
            disabled={!connected || group.members.length >= 32}
            onClick={onInvite}
          >
            <UserPlus size={18} />
          </button>
        )}
        <button
          className="icon"
          onClick={onClose}
          aria-label={t(isGroup ? "Close group chat" : "Close private chat")}
        >
          <X size={18} />
        </button>
      </div>
      <PeopleConversationPicker
        value={peer.id}
        people={people}
        unread={unread}
        height={floating.bounds.height}
        t={t}
        onSelect={onPeer}
      />
      <p className="direct-message-notice">
        {t("Live only. Messages are not saved and disappear when you refresh.")}
      </p>
      <div
        className="direct-message-log"
        ref={log}
        role="log"
        aria-label={isGroup ? peer.nickname : t("Private messages")}
        aria-live="polite"
        aria-relevant="additions"
        tabIndex={0}
      >
        {!messages.length && (
          <p className="direct-message-empty">
            {t(
              isRoom
                ? "Everyone online can read this group chat."
                : isGroup
                  ? "Only group members can read these messages. New members do not receive earlier messages."
                  : "Say hello. Only the two of you can see these messages.",
            )}
          </p>
        )}
        {messages.map((message) => (
          <article
            key={message.id}
            className={`direct-message${message.from === user ? " own" : ""}`}
          >
            {isGroup && message.from !== user && (
              <span className="group-message-author">
                {people.find((person) => person.id === message.from)
                  ?.nickname || message.from}
              </span>
            )}
            <p>{message.text}</p>
            <time dateTime={new Date(message.sent_at).toISOString()}>
              {new Date(message.sent_at).toLocaleTimeString(locale, {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </time>
          </article>
        ))}
      </div>
      {isGroup && !groupAvailable && (
        <p className="direct-message-status" role="status">
          {t("This group is no longer available to you.")}
        </p>
      )}
      {!connected ? (
        <p className="direct-message-status" role="status">
          {t("Chat disconnected. Wait for reconnection before sending.")}
        </p>
      ) : (
        !isGroup &&
        !status && (
          <p className="direct-message-status" role="status">
            {t("This person is offline. Messages are not queued.")}
          </p>
        )
      )}
      {error && (
        <p className="direct-message-status error" role="alert">
          {t(error)}
        </p>
      )}
      <form
        className="direct-message-composer"
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        <AutosizeTextarea
          autoFocus
          maxRows={Math.max(
            1,
            Math.min(6, Math.floor((floating.bounds.height - 290) / 20)),
          )}
          aria-label={t(isGroup ? "Group message" : "Private message")}
          placeholder={t(isGroup ? "Group message" : "Private message")}
          value={draft}
          maxLength={directMessageLimit}
          disabled={sending}
          onChange={(event) => {
            setDraft(event.target.value);
            setError("");
          }}
          onKeyDown={(event) => {
            if (
              event.key === "Enter" &&
              !event.shiftKey &&
              !event.nativeEvent.isComposing
            ) {
              event.preventDefault();
              void send();
            }
          }}
        />
        <button
          className="primary"
          type="submit"
          disabled={!writable || !draft.trim()}
          aria-label={t(
            isGroup ? "Send group message" : "Send private message",
          )}
        >
          {sending ? (
            <LoaderCircle className="stop-spinner" size={17} />
          ) : (
            <ArrowUp size={17} />
          )}
        </button>
      </form>
      {floatingWindowEdges.map((edge) => (
        <div
          key={edge}
          className="direct-message-edge"
          data-edge={edge}
          role={edge.length === 1 ? "separator" : undefined}
          aria-orientation={
            edge === "n" || edge === "s"
              ? "horizontal"
              : edge === "e" || edge === "w"
                ? "vertical"
                : undefined
          }
          aria-label={edge.length === 1 ? resizeLabel : undefined}
          aria-valuenow={
            edge.length === 1
              ? Math.round(
                  edge === "n" || edge === "s"
                    ? floating.bounds.height
                    : floating.bounds.width,
                )
              : undefined
          }
          aria-hidden={edge.length > 1 || undefined}
          tabIndex={edge.length === 1 ? 0 : -1}
          title={resizeLabel}
          {...floating.resizeHandle(edge)}
        />
      ))}
    </dialog>
  );
}
