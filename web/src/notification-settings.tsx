import { useEffect, useState } from "react";
import { Bell } from "lucide-react";
import type { Translator } from "./i18n";
import type { Locale } from "./preferences";
import {
  disablePush,
  enablePush,
  pushEnabled,
  PushError,
  pushSupport,
  testPush,
} from "./web-push";

export function NotificationSettings({
  t,
  language,
}: {
  t: Translator;
  language: Locale;
}) {
  const [enabled, setEnabled] = useState(false);
  const [busy, setBusy] = useState(true);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const support = pushSupport();
  useEffect(() => {
    let cancelled = false;
    void pushEnabled()
      .then((value) => {
        if (!cancelled) setEnabled(value);
      })
      .catch((error: unknown) => {
        if (!cancelled)
          setError(
            error instanceof Error ? error.message : t("Request failed"),
          );
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);
  async function run(action: "enable" | "disable" | "test") {
    if (busy) return;
    setBusy(true);
    setNotice("");
    setError("");
    try {
      if (action === "enable") {
        await enablePush(language);
        setEnabled(true);
      } else if (action === "disable") {
        await disablePush();
        setEnabled(false);
      } else {
        await testPush();
        setNotice(t("Test notification sent. Delivery may take a moment."));
      }
    } catch (error) {
      setError(
        error instanceof PushError
          ? t(error.key)
          : error instanceof Error
            ? error.message
            : t("Request failed"),
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section
      className="notification-settings"
      aria-labelledby="notification-title"
      aria-busy={busy}
    >
      <h3 id="notification-title">
        <Bell size={15} aria-hidden="true" />
        {t("Push notifications")}
      </h3>
      <p>
        {t(
          "Notify this device about private or group messages and task status. Notifications show sender and group names, but never message contents.",
        )}
      </p>
      <p>
        {t(
          "Private and group messages are live only and are not replayed after closing or refreshing the page.",
        )}
      </p>
      {support !== "supported" ? (
        <p>
          {t(
            support === "insecure"
              ? "Notifications require HTTPS (or localhost for development)."
              : "Push notifications are not supported in this browser. On iPhone or iPad, add Spool to the Home Screen first.",
          )}
        </p>
      ) : (
        <div className="notification-actions">
          <button
            type="button"
            disabled={busy}
            aria-pressed={enabled}
            onClick={() => void run(enabled ? "disable" : "enable")}
          >
            {t(enabled ? "Disable notifications" : "Enable notifications")}
          </button>
          {enabled && (
            <button
              type="button"
              disabled={busy}
              onClick={() => void run("test")}
            >
              {t("Send test notification")}
            </button>
          )}
        </div>
      )}
      {error && (
        <p className="notification-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
    </section>
  );
}
