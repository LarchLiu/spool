import type { MessageKey } from "./i18n";
import type { ServiceChangeTarget } from "./settings-client";
import type { PageProps } from "./settings-ui";
import { Check, RefreshCw } from "lucide-react";
import { usePendingSettings } from "./settings-pending";

export function SettingsChanges({
  data,
  run,
  t,
  disabled = false,
}: PageProps & { disabled?: boolean }) {
  const { saves, reveal, prepareServiceCancel } = usePendingSettings();
  const cancel = async (target: ServiceChangeTarget) => {
    const reset = prepareServiceCancel(target);
    const next = await run({ kind: "cancel-service-changes", target });
    if (next) reset(next.settings);
  };
  const pending =
    data.status.PendingChanges ??
    (data.status.RestartRequired ? ["runtime" as const] : []);
  if (!pending.length && !saves.length) return null;
  const titles: Record<ServiceChangeTarget, MessageKey> = {
    web: "Web access",
    relay: "Spool Relay",
    tsnet: "Tailscale",
    runtime: "Spool service",
  };
  const descriptions: Record<ServiceChangeTarget, MessageKey> = {
    web:
      data.settings.web_enabled !== false
        ? "Restart Web access to apply its access scope, port and public URL. Web clients reconnect; active tasks continue."
        : "Stop Web access. Desktop and other access entries stay available; active tasks continue.",
    relay: data.settings.relay_enabled
      ? "Restart Spool Relay and secure LAN access. Relay clients reconnect; active tasks continue."
      : "Stop Spool Relay and secure LAN access. Active tasks continue.",
    tsnet: data.settings.tsnet_enabled
      ? "Restart Tailscale to apply its node, HTTPS port and network settings. Tailscale clients reconnect; active tasks continue."
      : "Stop Tailscale access. Active tasks continue.",
    runtime:
      "Restart Spool to apply runtime settings. All access entries restart and active tasks are interrupted.",
  };
  return (
    <div
      className="settings-restart-bar"
      role="region"
      aria-label={t("Pending changes")}
    >
      {saves.map((save) => (
        <div
          className="settings-change-row"
          key={save.id}
          data-settings-save={save.id}
        >
          <div className="settings-change-description">
            <strong>{save.title}</strong>
            <p>{t("Unsaved changes")}</p>
          </div>
          <div className="settings-change-actions">
            <button
              type="button"
              disabled={disabled}
              data-cancel-settings-save={save.id}
              onClick={save.cancel}
            >
              {t("Cancel")}
            </button>
            <button
              type="button"
              className="settings-primary"
              disabled={disabled}
              data-apply-settings-save={save.id}
              onClick={() => {
                reveal(save.section);
                // Reveal before native validation focuses an invalid field.
                requestAnimationFrame(save.save);
              }}
            >
              <Check size={15} aria-hidden="true" />
              {save.label}
            </button>
          </div>
        </div>
      ))}
      {pending.map((target) => (
        <div
          className="settings-change-row"
          key={target}
          data-service-change={target}
        >
          <div className="settings-change-description">
            <strong>{t(titles[target])}</strong>
            <p>{t(descriptions[target])}</p>
          </div>
          <div className="settings-change-actions">
            <button
              type="button"
              disabled={disabled}
              data-cancel-service-change={target}
              onClick={() => void cancel(target)}
            >
              {t("Cancel")}
            </button>
            <button
              type="button"
              className="settings-primary"
              disabled={disabled}
              data-apply-service-change={target}
              onClick={() =>
                void run({ kind: "apply-service-changes", target })
              }
            >
              <RefreshCw size={15} aria-hidden="true" />
              {t("Apply changes")}
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
