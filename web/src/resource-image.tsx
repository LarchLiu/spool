import { useEffect, useState } from "react";
import type { Translator, MessageKey } from "./i18n";
import { imageFailureMessage } from "./image-resource";

// Shared by attachment cards, the image dialog and the file panel. Keep the
// browser's streaming/animation support; inspect status only after a failed load.
export function ResourceImage({ url, name, t, className, compact = false, loading, onFailure }: {
  url: string; name: string; t: Translator; className?: string; compact?: boolean;
  loading?: "lazy" | "eager"; onFailure?: () => void;
}) {
  const [attempt, setAttempt] = useState(0);
  const [failed, setFailed] = useState(false);
  const [message, setMessage] = useState<MessageKey>("Image could not be loaded. Try again.");
  useEffect(() => {
    if (!failed) return;
    const controller = new AbortController();
    if (url.startsWith("data:") || url.startsWith("blob:")) {
      setMessage("This file cannot be previewed as an image");
    } else {
      fetch(url, { method: "HEAD", credentials: "same-origin", cache: "no-store", signal: controller.signal })
        .then(response => { if (!controller.signal.aborted) setMessage(imageFailureMessage(response.ok ? 400 : response.status)); })
        .catch(() => {});
    }
    return () => controller.abort();
  }, [url, failed, attempt]);
  return <>
    <img key={attempt} src={url} alt={name} className={className} hidden={failed} loading={loading} decoding="async"
      onLoad={() => setFailed(false)} onError={() => { setFailed(true); onFailure?.(); }} />
    {failed && <span className="image-load-error" role="status">
      {compact ? t("Image unavailable: {name}", { name }) : t(message)}
      {!compact && <button type="button" onClick={() => { setFailed(false); setMessage("Image could not be loaded. Try again."); setAttempt(attempt + 1); }}>{t("Retry")}</button>}
    </span>}
  </>;
}
