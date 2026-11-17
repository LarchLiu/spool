import { useEffect, useMemo, useState } from "react";
import { appBasePath } from "./app-path";
import {
  parseSidebarPreferences,
  type SidebarPreferences,
  type SidebarSection,
} from "./sidebar-state";

function read(key: string | null) {
  try {
    return parseSidebarPreferences(key ? localStorage.getItem(key) : null);
  } catch {
    return parseSidebarPreferences(null);
  }
}

export function useSidebarPreferences(user: string | undefined) {
  const key = user
    ? appBasePath() + "spool.sidebar.v1:" + encodeURIComponent(user)
    : null;
  const restored = useMemo(() => read(key), [key]);
  const [state, setState] = useState({ key, value: restored });
  const preferences = state.key === key ? state.value : restored;
  useEffect(() => {
    if (!key) return;
    try {
      localStorage.setItem(key, JSON.stringify(preferences));
    } catch {
      // Preferences remain usable when device storage is disabled.
    }
  }, [key, preferences]);

  function update(change: (old: SidebarPreferences) => SidebarPreferences) {
    setState((old) => ({
      key,
      value: change(old.key === key ? old.value : restored),
    }));
  }

  return {
    ...preferences,
    togglePin(chat: string) {
      update((old) => {
        const pinned = old.pinned.includes(chat);
        return {
          pinned: pinned
            ? old.pinned.filter((id) => id !== chat)
            : [chat, ...old.pinned],
          collapsed: pinned
            ? old.collapsed
            : { ...old.collapsed, pinned: false },
        };
      });
    },
    toggleSection(section: SidebarSection) {
      update((old) => ({
        ...old,
        collapsed: { ...old.collapsed, [section]: !old.collapsed[section] },
      }));
    },
  };
}
