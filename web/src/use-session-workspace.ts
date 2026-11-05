import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import type { SessionWorkspace } from "./types";

export function useSessionWorkspace(chatID?: string, revision?: number) {
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<{
    chatID: string;
    workspace?: SessionWorkspace;
    error?: string;
  }>();
  const reload = useCallback(() => setRefresh((value) => value + 1), []);
  useEffect(() => {
    if (!chatID) return;
    const controller = new AbortController();
    let pending = false;
    const load = async () => {
      if (pending || controller.signal.aborted) return;
      pending = true;
      try {
        const workspace = await api<SessionWorkspace>(
          `/chats/${encodeURIComponent(chatID)}/workspace`,
          undefined,
          controller.signal,
        );
        if (!controller.signal.aborted) setState({ chatID, workspace });
      } catch (err) {
        if (!controller.signal.aborted)
          setState({ chatID, error: err instanceof Error ? err.message : "" });
      } finally {
        pending = false;
      }
    };
    void load();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 10_000);
    window.addEventListener("focus", load);
    return () => {
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener("focus", load);
    };
  }, [chatID, revision, refresh]);
  return {
    ...(state?.chatID === chatID ? state : {}),
    reload,
  };
}
