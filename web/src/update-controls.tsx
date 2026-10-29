import type { Translator } from "./i18n";
import type { UpdatePreferences } from "./settings-client";

const checkIntervals = [
  [30, "30 minutes"],
  [60, "1 hour"],
  [360, "6 hours"],
  [720, "12 hours"],
  [1440, "24 hours"],
] as const;

export function UpdateCheckInterval({
  value,
  disabled,
  onChange,
  t,
}: {
  value: UpdatePreferences["check_interval_minutes"];
  disabled: boolean;
  onChange: (minutes: UpdatePreferences["check_interval_minutes"]) => void;
  t: Translator;
}) {
  return (
    <div
      className="settings-check-interval"
      role="group"
      aria-label={t("Check interval")}
    >
      {checkIntervals.map(([minutes, label]) => (
        <button
          key={minutes}
          type="button"
          aria-pressed={value === minutes}
          disabled={disabled}
          onClick={() => {
            if (value !== minutes) onChange(minutes);
          }}
        >
          {t(label)}
        </button>
      ))}
    </div>
  );
}

export function UpdateIndicator({ t }: { t: Translator }) {
  return (
    <span
      className="update-indicator"
      role="img"
      aria-label={t("Update available")}
      title={t("Update available")}
    />
  );
}
