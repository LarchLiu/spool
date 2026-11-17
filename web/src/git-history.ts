import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { GitView } from "./git-panel-state";
import type { Translator } from "./i18n";

function appendPage(first: GitView, page: GitView): GitView {
  const seen = new Set(first.commits.map((item) => item.id));
  return {
    ...first,
    commits: [
      ...first.commits,
      ...page.commits.filter((item) => !seen.has(item.id)),
    ],
    next_cursor: page.next_cursor,
  };
}

/** One directory-scoped history snapshot, with refresh and pagination serialized. */
export function useGitHistory(
  url: string,
  active: boolean,
  refresh: number,
  retry: number,
  t: Translator,
) {
  const [data, setData] = useState<GitView>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [moreLoading, setMoreLoading] = useState(false);
  const [moreError, setMoreError] = useState("");
  const cache = useRef<{ url: string; data: GitView } | undefined>(undefined);
  const more = useRef<(() => void) | undefined>(undefined);
  const loadMore = useCallback(() => more.current?.(), []);

  useEffect(() => {
    setLoading(false);
    setMoreLoading(false);
    if (!active) return;
    const controller = new AbortController();
    let pending = false;
    let snapshot = cache.current?.url === url ? cache.current.data : undefined;
    setData(snapshot);
    setError("");
    setMoreError("");
    const accept = (next: GitView) => {
      snapshot = next;
      cache.current = { url, data: next };
      setData(next);
    };
    const request = (cursor?: string) =>
      api<GitView>(
        url + (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""),
        undefined,
        controller.signal,
      );
    const load = async (additional = false) => {
      if (
        pending ||
        controller.signal.aborted ||
        (additional && !snapshot?.next_cursor)
      )
        return;
      pending = true;
      if (additional) {
        setMoreLoading(true);
        setMoreError("");
      } else setLoading(true);
      try {
        let next = await request(
          additional ? snapshot?.next_cursor : undefined,
        );
        if (controller.signal.aborted) return;
        if (additional && snapshot) next = appendPage(snapshot, next);
        else if (snapshot && snapshot.commit === next.commit) {
          // Refresh every loaded page so tag and upstream changes also update
          // older visible rows, while retaining their scroll position.
          const loadedCount = snapshot.commits.length;
          while (next.next_cursor && next.commits.length < loadedCount) {
            const page = await request(next.next_cursor);
            if (controller.signal.aborted) return;
            next = appendPage(next, page);
          }
        }
        accept(next);
        setError("");
        setMoreError("");
      } catch (err) {
        if (!controller.signal.aborted) {
          const message =
            err instanceof Error ? err.message : t("Request failed");
          if (additional) setMoreError(message);
          else setError(message);
        }
      } finally {
        pending = false;
        if (!controller.signal.aborted) {
          setLoading(false);
          setMoreLoading(false);
        }
      }
    };
    more.current = () => {
      void load(true);
    };
    void load();
    const poll = () => {
      if (document.visibilityState === "visible") void load();
    };
    const timer = window.setInterval(poll, 10_000);
    window.addEventListener("focus", poll);
    return () => {
      controller.abort();
      more.current = undefined;
      clearInterval(timer);
      window.removeEventListener("focus", poll);
    };
  }, [url, active, refresh, retry, t]);

  return { data, loading, error, moreLoading, moreError, loadMore };
}
