import { Settings2 } from "lucide-react";
import type { Translator } from "./i18n";
import { desktopSettingsPath, isDesktopChat } from "./runtime.js";

export function DesktopSettingsLink({
  t,
  className = "",
  iconOnly = false,
}: {
  t: Translator;
  className?: string;
  iconOnly?: boolean;
}) {
  if (!isDesktopChat()) return null;
  return (
    <a
      className={`icon desktop-settings ${className}`}
      href={desktopSettingsPath}
      aria-label={t("Settings")}
      title={t("Settings")}
    >
      <Settings2 size={16} />
      {!iconOnly && <span>{t("Settings")}</span>}
    </a>
  );
}

export function ChatSettingsControl({
  t,
  onAppearance,
}: {
  t: Translator;
  onAppearance: () => void;
}) {
  if (isDesktopChat()) return <DesktopSettingsLink t={t} iconOnly />;
  return (
    <button
      className="icon"
      onClick={onAppearance}
      aria-label={t("Appearance and language")}
      title={t("Settings")}
    >
      <Settings2 size={16} />
    </button>
  );
}
