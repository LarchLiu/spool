import { CornerDownRight, ListEnd, Trash2 } from "lucide-react";
import type { Translator } from "./i18n";
import type { QueuedMessage } from "./types";

// Messages sent while the agent was still answering. Each waits for the
// running turn to end unless it is steered into that turn or removed.
export function QueuedMessages({
  messages,
  running,
  busy,
  t,
  onSteer,
  onDelete,
}: {
  messages: readonly QueuedMessage[];
  running: boolean;
  busy: boolean;
  t: Translator;
  onSteer: (id: string) => void;
  onDelete: (id: string) => void;
}) {
  if (!messages.length) return null;
  return (
    <ul className="queued-messages" aria-label={t("Queued messages")}>
      {messages.map((message) => (
        <li className="queued-message" key={message.id}>
          <ListEnd size={15} aria-hidden="true" />
          <span className="queued-message-text">
            {message.message.trim() || message.attachments?.join(", ")}
          </span>
          <button
            type="button"
            className="queued-message-steer"
            disabled={busy}
            onClick={() => onSteer(message.id)}
          >
            <CornerDownRight size={14} aria-hidden="true" />
            {t(running ? "Steer" : "Send now")}
          </button>
          <button
            type="button"
            className="icon"
            disabled={busy}
            aria-label={t("Delete queued message")}
            title={t("Delete queued message")}
            onClick={() => onDelete(message.id)}
          >
            <Trash2 size={15} />
          </button>
        </li>
      ))}
    </ul>
  );
}

// A key that belongs to an input method choosing text, not to the composer.
// WebKit reports the Enter that confirms a choice after the composition has
// ended, marked only by key code 229.
export function inputMethodKey(event: {
  isComposing: boolean;
  keyCode: number;
}) {
  return event.isComposing || event.keyCode === 229;
}

// Enter sends and Shift+Enter breaks the line. A touch keyboard has no
// Shift+Enter, so there Enter keeps its line break and the button sends.
export function composerEnterAction(
  event: {
    key: string;
    shiftKey: boolean;
    metaKey: boolean;
    ctrlKey: boolean;
    altKey: boolean;
    isComposing: boolean;
    keyCode: number;
  },
  touchKeyboard: boolean,
): "send" | "steer" | null {
  if (inputMethodKey(event)) return null;
  if (event.key !== "Enter" || event.shiftKey) return null;
  if (event.metaKey || event.ctrlKey) return "steer";
  if (event.altKey) return "send";
  return touchKeyboard ? null : "send";
}
