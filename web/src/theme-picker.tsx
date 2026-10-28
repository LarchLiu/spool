import { Monitor, Moon, Sun } from "lucide-react";
import type { Translator } from "./i18n";
import type { Theme } from "./preferences";

const themes = [
  ["light", "Light", Sun],
  ["dark", "Dark", Moon],
  ["system", "Follow system", Monitor],
] as const;

export function ThemePicker({
  value,
  onChange,
  t,
}: {
  value: Theme;
  onChange: (theme: Theme) => void;
  t: Translator;
}) {
  return (
    <div className="settings-theme-picker" role="group" aria-label={t("Theme")}>
      {themes.map(([theme, label, Icon]) => (
        <button
          key={theme}
          type="button"
          className="icon"
          data-theme-choice={theme}
          aria-label={t(label)}
          title={t(label)}
          aria-pressed={value === theme}
          onClick={() => {
            if (value !== theme) onChange(theme);
          }}
        >
          <Icon size={18} aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}
