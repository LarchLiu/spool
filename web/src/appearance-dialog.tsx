import { useEffect, useRef, type ReactNode } from "react";
import { Check, Monitor, Moon, Sun, X } from "lucide-react";
import type { Translator } from "./i18n";
import { CustomSelect } from "./custom-select.js";
import {
  languages,
  type Language,
  type Theme,
  type UIPreferences,
} from "./preferences.js";

export function AppearanceDialog({
  preferences,
  saving,
  version,
  t,
  onChange,
  onClose,
  children,
}: {
  preferences: UIPreferences;
  saving: boolean;
  version: string;
  t: Translator;
  onChange: (preferences: UIPreferences) => void;
  onClose: () => void;
  children?: ReactNode;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  const themes = [
    ["light", "Light", Sun],
    ["dark", "Dark", Moon],
    ["system", "Follow system", Monitor],
  ] as const;
  return (
    <dialog
      className="appearance-dialog"
      ref={dialog}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          const r = event.currentTarget.getBoundingClientRect();
          if (
            event.clientX < r.left ||
            event.clientX > r.right ||
            event.clientY < r.top ||
            event.clientY > r.bottom
          )
            onClose();
        }
      }}
      aria-labelledby="appearance-title"
    >
      <div className="dialog-heading">
        <h2 id="appearance-title">{t("Appearance and language")}</h2>
        <button
          autoFocus
          className="icon"
          aria-label={t("Close settings")}
          onClick={onClose}
        >
          <X size={19} />
        </button>
      </div>
      <fieldset disabled={saving}>
        <legend>{t("Theme")}</legend>
        <div className="theme-options">
          {themes.map(([value, label, Icon]) => (
            <button
              key={value}
              type="button"
              aria-pressed={preferences.theme === value}
              onClick={() =>
                onChange({ ...preferences, theme: value as Theme })
              }
            >
              <Icon size={21} />
              <span>{t(label)}</span>
              {preferences.theme === value && (
                <Check className="selected-check" size={12} />
              )}
            </button>
          ))}
        </div>
      </fieldset>
      <div className="language-choice">
        <span>{t("Language")}</span>
        <CustomSelect
          label={t("Language")}
          value={preferences.language}
          disabled={saving}
          onChange={(value) =>
            onChange({
              ...preferences,
              language: value as Language,
            })
          }
          options={[
            { value: "system", label: t("Follow system") },
            ...languages.map(([value, label]) => ({ value, label })),
          ]}
        />
      </div>
      {children}
      <p>{t("Current version: {version}", { version: version || "..." })}</p>
    </dialog>
  );
}
