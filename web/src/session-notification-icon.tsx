import { CircleCheck, CircleX, ShieldQuestion } from "lucide-react";
import type { Translator } from "./i18n";
import type { SessionNotificationKind } from "./runtime";

export function SessionNotificationIcon({
  kind,
  t,
}: {
  kind: SessionNotificationKind | undefined;
  t: Translator;
}) {
  if (!kind) return null;
  const Icon = {
    completed: CircleCheck,
    failed: CircleX,
    approval: ShieldQuestion,
  }[kind];
  const label = t(
    (
      {
        completed: "Completed",
        failed: "Failed",
        approval: "Approval required",
      } as const
    )[kind],
  );
  return (
    <span
      className="session-notification-icon"
      data-status={kind}
      role="img"
      aria-label={label}
      title={label}
    >
      <Icon size={14} aria-hidden="true" />
    </span>
  );
}
