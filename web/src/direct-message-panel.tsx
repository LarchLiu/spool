import { useEffect, useRef, useState } from "react";
import { ArrowUp, LoaderCircle, UserPlus, X } from "lucide-react";
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
import { PeopleConversationList } from "./people-conversation-list.js";
import {
  floatingWindowEdges,
  type FloatingWindowBounds,
} from "./floating-window.js";

type DirectMessagePanelProps = {
  user: string;
  self?: AvatarIdentity;
  peer: AvatarIdentity;
  people: AvatarIdentity[];
  groups?: PeopleChatGroup[];
  resolvePerson?: (id: string) => AvatarIdentity;
  presence?: Record<string, string>;
  unread: Record<string, number>;
  messages: DirectMessage[];
  connected: boolean;
  status?: string;
  onlineCount?: number;
  group?: PeopleChatGroup;
  draft: string;
  sending: boolean;
  locale: string;
  t: Translator;
  onSend: (to: string, text: string) => Promise<DirectMessage>;
  onDraftChange: (text: string) => void;
  onPeer: (id: string) => void;
  onClose: () => void;
  onInvite?: () => void;
  onNewGroup?: () => void;
  windowBounds?: FloatingWindowBounds | null;
  onWindowBoundsChange?: (bounds: FloatingWindowBounds) => void;
};

export function DirectMessagePanel(props: DirectMessagePanelProps) {
  const {
    peer,
    people,
    groups = props.group ? [props.group] : [],
    resolvePerson,
    presence,
    unread,
    connected,
    status,
    onlineCount = 0,
    group,
    t,
    onPeer,
    onClose,
    onInvite,
    onNewGroup,
    windowBounds,
    onWindowBoundsChange,
  } = props;
  const dialog = useRef<HTMLDialogElement>(null);
  const floating = useFloatingWindow(windowBounds, onWindowBoundsChange);
  const moveLabel = t("Move chat window (arrow keys)");
  const resizeLabel = t("Resize chat window (arrow keys)");
  const isGroup = isPeopleGroup(peer.id);
  const isRoom = peer.id === onlineChatRoom;
  useEffect(() => {
    dialog.current?.show();
    dialog.current?.querySelector("textarea")?.focus({ preventScroll: true });
  }, []);
  return (
    <dialog
      ref={dialog}
      className="direct-message-panel"
      aria-label={peer.nickname}
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
      <div className="direct-message-body">
        <PeopleConversationList
          value={peer.id}
          people={people}
          groups={groups}
          resolvePerson={resolvePerson}
          presence={presence}
          connected={connected}
          unread={unread}
          t={t}
          onSelect={onPeer}
          onNewGroup={onNewGroup}
        />
        <div className="direct-message-main">
          <div className="direct-message-heading direct-message-toolbar">
            <div
              className="direct-message-drag"
              role="button"
              tabIndex={0}
              aria-label={moveLabel}
              title={moveLabel}
              {...floating.moveHandle}
            >
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
              aria-label={t(
                isGroup ? "Close group chat" : "Close private chat",
              )}
            >
              <X size={18} />
            </button>
          </div>
          <DirectMessageContent
            key={peer.id}
            {...props}
            height={floating.bounds.height}
          />
        </div>
      </div>
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

function DirectMessageContent({
  user,
  self,
  peer,
  people,
  messages,
  connected,
  status,
  group,
  draft,
  sending,
  locale,
  t,
  onSend,
  onDraftChange,
  height,
}: DirectMessagePanelProps & { height: number }) {
  const content = useRef<HTMLDivElement>(null);
  const log = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<MessageKey | "">("");
  const composing = useRef(false);
  const [isComposing, setIsComposing] = useState(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    content.current?.querySelector("textarea")?.focus({ preventScroll: true });
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
        active === content.current?.querySelector('button[type="submit"]'))
    )
      content.current?.querySelector("textarea")?.focus();
  }, [sending]);
  const isGroup = isPeopleGroup(peer.id);
  const isRoom = peer.id === onlineChatRoom;
  const groupAvailable = isRoom || !!group;
  const tooLong = [...draft].length > directMessageLimit;
  const writable =
    connected && (isGroup ? groupAvailable : !!status) && !sending && !tooLong;
  const send = async () => {
    if (!writable || composing.current || !draft.trim()) return;
    setError("");
    try {
      await onSend(peer.id, draft);
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
    }
  };
  return (
    <div className="direct-message-content" ref={content}>
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
        {messages.map((message) => {
          const own = message.from === user;
          const author = (own
            ? self
            : people.find((person) => person.id === message.from)) || {
            id: message.from,
            nickname: message.from,
          };
          return (
            <article
              key={message.id}
              className={`direct-message${own ? " own" : ""}`}
              data-sender={message.from}
            >
              <span
                className="direct-message-avatar"
                role="img"
                aria-label={author.nickname}
                title={author.nickname}
              >
                <UserAvatar person={author} size={28} />
              </span>
              <div className="direct-message-entry">
                <div className="direct-message-bubble">
                  <p>{message.text}</p>
                </div>
                <time dateTime={new Date(message.sent_at).toISOString()}>
                  {new Date(message.sent_at).toLocaleTimeString(locale, {
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                </time>
              </div>
            </article>
          );
        })}
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
      {(error || (tooLong && !isComposing)) && (
        <p className="direct-message-status error" role="alert">
          {t(
            tooLong
              ? "Enter a message of up to 2000 characters."
              : (error as MessageKey),
          )}
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
          maxRows={Math.max(1, Math.min(6, Math.floor((height - 290) / 20)))}
          aria-label={t(isGroup ? "Group message" : "Private message")}
          placeholder={t(isGroup ? "Group message" : "Private message")}
          value={draft}
          aria-invalid={(tooLong && !isComposing) || undefined}
          disabled={sending}
          onChange={(event) => {
            onDraftChange(event.target.value);
            setError("");
          }}
          onCompositionStart={() => {
            composing.current = true;
            setIsComposing(true);
          }}
          onCompositionEnd={() => {
            composing.current = false;
            setIsComposing(false);
          }}
          onKeyDown={(event) => {
            if (
              event.key === "Enter" &&
              !event.shiftKey &&
              !composing.current &&
              !event.nativeEvent.isComposing &&
              // WebKit can end composition before the confirming Enter keydown.
              event.nativeEvent.keyCode !== 229
            ) {
              event.preventDefault();
              void send();
            }
          }}
        />
        <button
          className="primary"
          type="submit"
          disabled={!writable || isComposing || !draft.trim()}
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
    </div>
  );
}
