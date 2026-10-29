import { useSyncExternalStore } from "react";
import { Settings2 } from "lucide-react";
import type { Translator } from "./i18n";
import {
  desktopSettingsPath,
  isDesktopChat,
  desktopUpdateAvailable,
  subscribeDesktopUpdates,
} from "./runtime.js";
import { UpdateIndicator } from "./update-controls.js";

export function DesktopSettingsLink({
  t,
  className = "",
  iconOnly = false,
}: {
  t: Translator;
  className?: string;
  iconOnly?: boolean;
}) {
  const updateAvailable = useSyncExternalStore(
    subscribeDesktopUpdates,
    desktopUpdateAvailable,
    desktopUpdateAvailable,
  );
  if (!isDesktopChat()) return null;
  return (
    <a
      className={`icon desktop-settings ${className}${iconOnly ? "" : " desktop-settings-labeled"}`}
      href={desktopSettingsPath}
      aria-label={t("Settings")}
      title={t("Settings")}
    >
      <Settings2 size={16} />
      {updateAvailable && <UpdateIndicator t={t} />}
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
