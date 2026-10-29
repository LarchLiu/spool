import { useEffect, useState, useSyncExternalStore } from "react";
import { ArrowUpRight, ChevronDown, RefreshCw } from "lucide-react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Translator } from "./i18n";
import { settingsCall, type ReleaseHistory } from "./settings-client.js";
import { ReleaseHistoryLoader } from "./release-history.js";

function releaseLink(href: string | undefined, base: string) {
  if (!href || !defaultUrlTransform(href)) return;
  try {
    const url = new URL(href, base);
    if (url.protocol === "https:" || url.protocol === "http:") return url.href;
  } catch {
    return;
  }
}

export function ReleaseHistoryVersions({
  history,
  t,
}: {
  history: ReleaseHistory;
  t: Translator;
}) {
  return (
    <div className="settings-release-versions">
      {!history.releases.length && (
        <p className="settings-hint">
          {t("No releases were found in this version range.")}
        </p>
      )}
      {history.releases.map((release) => (
        <article className="settings-release-version" key={release.version}>
          <header>
            <h3>v{release.version}</h3>
            {release.prerelease && <span>{t("Pre-release")}</span>}
            {release.published_at &&
              !Number.isNaN(Date.parse(release.published_at)) && (
                <time dateTime={release.published_at}>
                  {release.published_at.slice(0, 10)}
                </time>
              )}
            <a
              href={releaseLink(release.url, "https://github.com/")}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={t("View {version} on GitHub", {
                version: release.version,
              })}
              title={t("View {version} on GitHub", {
                version: release.version,
              })}
            >
              GitHub <ArrowUpRight size={13} aria-hidden="true" />
            </a>
          </header>
          {release.name &&
            release.name !== `v${release.version}` &&
            release.name !== release.version && (
              <p className="settings-release-name">{release.name}</p>
            )}
          {release.body.trim() ? (
            <div className="markdown settings-release-markdown">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                skipHtml
                components={{
                  a: ({ href, children }) => {
                    const url = releaseLink(href, release.url);
                    return url ? (
                      <a href={url} target="_blank" rel="noopener noreferrer">
                        {children}
                      </a>
                    ) : (
                      <span>{children}</span>
                    );
                  },
                  img: ({ src, alt }) => {
                    const url = releaseLink(
                      typeof src === "string" ? src : undefined,
                      release.url,
                    );
                    return url ? (
                      <a href={url} target="_blank" rel="noopener noreferrer">
                        {alt || t("Image")}
                      </a>
                    ) : (
                      <span>{alt}</span>
                    );
                  },
                  table: ({ children }) => (
                    <div className="markdown-table">
                      <table>{children}</table>
                    </div>
                  ),
                }}
              >
                {release.body}
              </ReactMarkdown>
            </div>
          ) : (
            <p className="settings-hint">
              {t("No release notes were provided for this version.")}
            </p>
          )}
        </article>
      ))}
    </div>
  );
}

export function ReleaseNotesPanel({
  current,
  target,
  t,
}: {
  current: string;
  target: string;
  t: Translator;
}) {
  const [expanded, setExpanded] = useState(false);
  const [loader] = useState(
    () =>
      new ReleaseHistoryLoader(current, target, () =>
        settingsCall<ReleaseHistory>("ReleaseNotes"),
      ),
  );
  const state = useSyncExternalStore(
    loader.subscribe,
    loader.getState,
    loader.getState,
  );
  useEffect(() => {
    if (expanded) void loader.load();
  }, [expanded, loader]);
  return (
    <section
      className="settings-group settings-release-history"
      aria-label={t("Release notes")}
    >
      <div className="settings-card">
        <button
          type="button"
          className="settings-release-toggle"
          aria-expanded={expanded}
          aria-controls="update-release-history"
          onClick={() => setExpanded((value) => !value)}
        >
          <span>
            <strong>{t("View release notes")}</strong>
            <small>
              {t("From {current} to {target}", { current, target })}
            </small>
          </span>
          <ChevronDown size={17} aria-hidden="true" />
        </button>
        {expanded && (
          <div
            id="update-release-history"
            className="settings-release-body"
            aria-busy={state.loading}
          >
            <div className="settings-release-source">
              <span>
                {t("All published releases in this range, newest first.")}
              </span>
              <button
                type="button"
                className="icon"
                disabled={state.loading}
                onClick={() => void loader.load(true)}
                aria-label={t("Refresh release notes")}
                title={t("Refresh release notes")}
              >
                <RefreshCw
                  size={15}
                  className={state.loading ? "settings-spinning" : undefined}
                />
              </button>
            </div>
            {state.loading && (
              <p role="status" className="settings-hint">
                {t("Loading releases from GitHub…")}
              </p>
            )}
            {state.error && (
              <div className="settings-error" role="alert">
                <p>
                  {t(
                    "Could not load release notes. Check your network or GitHub rate limit, then retry.",
                  )}
                </p>
                <p>{state.error}</p>
                <button type="button" onClick={() => void loader.load(true)}>
                  {t("Retry")}
                </button>
              </div>
            )}
            {state.history && (
              <ReleaseHistoryVersions history={state.history} t={t} />
            )}
          </div>
        )}
      </div>
    </section>
  );
}
