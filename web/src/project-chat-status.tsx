import type { Translator } from "./i18n";
import type { Chat } from "./types";
import type { SessionNotifications } from "./use-session-notifications";
import { SessionNotificationIcon } from "./session-notification-icon";

export function ProjectChatStatus({
  collapsed,
  chats,
  notifications,
  t,
}: {
  collapsed: boolean;
  chats: Chat[];
  notifications: SessionNotifications;
  t: Translator;
}) {
  if (!collapsed) return null;
  const running = chats.some((chat) => chat.status === "running");
  const kinds = new Set(chats.map((chat) => notifications[chat.id]));
  const pending = (["approval", "failed", "completed"] as const).filter(
    (kind) => kinds.has(kind),
  );
  if (!running && !pending.length) return null;
  return (
    <span className="project-chat-status">
      {running && (
        <span
          className="task-spinner"
          role="status"
          aria-label={t("Working")}
          title={t("Working")}
        />
      )}
      {pending.map((kind) => (
        <SessionNotificationIcon key={kind} kind={kind} t={t} />
      ))}
    </span>
  );
}
