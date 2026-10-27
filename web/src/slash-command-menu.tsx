import { Crosshair } from "lucide-react";
import type { Translator } from "./i18n";

export function SlashCommandMenu({
  t,
  onSelect,
}: {
  t: Translator;
  onSelect: () => void;
}) {
  return (
    <div
      className="slash-command-menu"
      id="slash-command-menu"
      role="listbox"
      aria-label={t("Chat commands")}
    >
      <button
        id="slash-command-goal"
        type="button"
        role="option"
        aria-selected="true"
        onMouseDown={(event) => event.preventDefault()}
        onClick={onSelect}
      >
        <Crosshair size={18} aria-hidden="true" />
        <span>
          <strong>/goal</strong>
          <small>{t("Set a native agent goal")}</small>
        </span>
        <kbd>Tab</kbd>
      </button>
    </div>
  );
}
