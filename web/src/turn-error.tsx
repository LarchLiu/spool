import { useState } from "react";
import { X } from "lucide-react";
import type { Translator } from "./i18n";
import type { Turn } from "./types";

export function TurnError({ turn, t }: { turn: Turn; t: Translator }) {
  if (!turn.error || turn.status === "stopped") return null;
  return (
    <ErrorNotice
      key={JSON.stringify([turn.error, turn.finished_at])}
      message={turn.error}
      t={t}
    />
  );
}

function ErrorNotice({ message, t }: { message: string; t: Translator }) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed) return null;
  return (
    <div className="error dismissible-error" role="alert">
      <span>{message}</span>
      <button
        type="button"
        className="icon"
        onClick={() => setDismissed(true)}
        aria-label={t("Dismiss")}
        title={t("Dismiss")}
      >
        <X size={15} />
      </button>
    </div>
  );
}
