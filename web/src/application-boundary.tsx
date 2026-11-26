import { useState, type ReactNode } from "react";
import { translator } from "./i18n";
import { resolveLanguage } from "./preferences";
import { desktopLocale } from "./runtime";
import { RenderBoundary } from "./render-boundary";

function ApplicationError({ onRetry }: { onRetry: () => void }) {
  // Do not depend on the failed application's hooks or API to render recovery.
  const t = translator(
    desktopLocale() ??
      resolveLanguage("system", [
        document.documentElement.lang,
        ...navigator.languages,
      ]),
  );
  return (
    <main className="application-error" role="alert">
      <h1>{t("The page could not be displayed.")}</h1>
      <p>{t("Try again to restore the workspace.")}</p>
      <div>
        <button onClick={onRetry}>{t("Retry")}</button>
        <button onClick={() => location.reload()}>{t("Reload")}</button>
      </div>
    </main>
  );
}

export function ApplicationBoundary({ children }: { children: ReactNode }) {
  const [revision, setRevision] = useState(0);
  return (
    <RenderBoundary
      key={revision}
      application
      fallback={
        <ApplicationError onRetry={() => setRevision((value) => value + 1)} />
      }
    >
      {children}
    </RenderBoundary>
  );
}
