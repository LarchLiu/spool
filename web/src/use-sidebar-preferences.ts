import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { appBasePath } from "./app-path";
import { api } from "./api";
import { parseSidebarPreferences } from "./sidebar-state";
import { createSidebarSync, type SidebarSnapshot } from "./sidebar-sync";

function read(key: string | null) {
  try {
    return parseSidebarPreferences(key ? localStorage.getItem(key) : null);
  } catch {
    return parseSidebarPreferences(null);
  }
}

export function useSidebarPreferences(
  user: string | undefined,
  failed: (error: unknown) => void,
) {
  const key = user
    ? appBasePath() + "spool.sidebar.v1:" + encodeURIComponent(user)
    : null;
  const onError = useRef(failed);
  onError.current = failed;
  const sync = useMemo(
    () =>
      createSidebarSync(
        read(key),
        (change, signal) =>
          api<SidebarSnapshot>("/sidebar-preferences", change, signal),
        (error) => onError.current(error),
      ),
    [key],
  );
  const preferences = useSyncExternalStore(sync.subscribe, sync.snapshot);
  useEffect(() => {
    if (key) sync.start();
    return () => sync.stop();
  }, [key, sync]);
  useEffect(() => {
    if (!key) return;
    try {
      localStorage.setItem(key, JSON.stringify(preferences));
    } catch {
      // Preferences remain usable when device storage is disabled.
    }
  }, [key, preferences]);

  return {
    ...preferences,
    accept: sync.accept,
    togglePin: sync.togglePin,
    toggleSection: sync.toggleSection,
  };
}
