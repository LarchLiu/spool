import { CircleAlert, LoaderCircle } from "lucide-react";
import type { Translator } from "./i18n";
import type { Chat } from "./types";
import type { DraftAttachment } from "./attachment-selection";

export type LocalDelivery = {
  id: string;
  owner?: string;
  chatID: string;
  first?: { chatID: string };
  turnID: string;
  delivery: "queue" | "steer";
  message: string;
  files: DraftAttachment[];
  createdAt: number;
  status: "sending" | "failed";
  retired?: boolean;
};

// A provisional steer can be streamed before the provider acknowledges it.
// It occupies the server's position but still needs the local sending marker.
export function deliveryReceipt(chat: Chat, id: string) {
  if (chat.queued_messages?.some((message) => message.client_message_id === id))
    return { present: true, accepted: true };
  const parts = chat.transcript.parts.filter(
    (part) => part.role === "user" && part.client_message_id === id,
  );
  return {
    present: !!parts.length,
    accepted: parts.some((part) => !part.pending),
  };
}

export function DeliveryStatus({
  status,
  t,
  onEdit,
}: {
  status: "sending" | "failed";
  t: Translator;
  onEdit?: () => void;
}) {
  return (
    <span className={"message-delivery-status " + status} role="status">
      {status === "sending" ? (
        <LoaderCircle size={12} aria-hidden="true" />
      ) : (
        <CircleAlert size={12} aria-hidden="true" />
      )}
      <span>{t(status === "sending" ? "Sending…" : "Send failed")}</span>
      {status === "failed" && onEdit && (
        <button type="button" onClick={onEdit}>
          {t("Edit message")}
        </button>
      )}
    </span>
  );
}
