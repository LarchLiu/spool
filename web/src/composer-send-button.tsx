import { ArrowUp, LoaderCircle, Square } from "lucide-react";
import type { Translator } from "./i18n";

export function ComposerSendButton({
  active,
  stopping,
  disabled,
  t,
  onStop,
}: {
  active: boolean;
  stopping: boolean;
  disabled: boolean;
  t: Translator;
  onStop: () => void;
}) {
  const label = t(active ? (stopping ? "Stopping…" : "Stop response") : "Send message");
  return (
    <button
      className="send"
      type={active ? "button" : "submit"}
      aria-label={label}
      title={label}
      aria-busy={stopping || undefined}
      onClick={active ? onStop : undefined}
      disabled={disabled}
    >
      {active ? (
        stopping ? (
          <LoaderCircle className="stop-spinner" size={18} />
        ) : (
          <Square size={14} fill="currentColor" />
        )
      ) : (
        <ArrowUp size={20} />
      )}
    </button>
  );
}
