import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import type { SessionWorkspaceView } from "./types";
import {
  receiveSessionWorkspace,
  selectSessionWorkspace,
  type WorkspaceSelection,
} from "./session-workspace-state";

export function useSessionWorkspace(chatID?: string, revision?: number) {
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<{
    chatID: string;
    selection?: WorkspaceSelection;
    error?: string;
  }>();
  const reload = useCallback(() => setRefresh((value) => value + 1), []);
  const select = useCallback(
    (path: string) =>
      setState((old) =>
        old && old.chatID === chatID && old.selection
          ? { ...old, selection: selectSessionWorkspace(old.selection, path) }
          : old,
      ),
    [chatID],
  );
  useEffect(() => {
    if (!chatID) return;
    const controller = new AbortController();
    let pending = false;
    const load = async () => {
      if (pending || controller.signal.aborted) return;
      pending = true;
      try {
        const workspace = await api<SessionWorkspaceView>(
          `/chats/${encodeURIComponent(chatID)}/workspace`,
          undefined,
          controller.signal,
        );
        if (!controller.signal.aborted)
          setState((old) => ({
            chatID,
            selection: receiveSessionWorkspace(
              old?.chatID === chatID ? old.selection : undefined,
              workspace,
            ),
          }));
      } catch (err) {
        if (!controller.signal.aborted)
          setState((old) => ({
            chatID,
            selection: old?.chatID === chatID ? old.selection : undefined,
            error: err instanceof Error ? err.message : "",
          }));
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
    workspace:
      state && state.chatID === chatID && !state.error
        ? state.selection?.workspace
        : undefined,
    workspaces:
      state && state.chatID === chatID && !state.error
        ? state.selection?.latest.workspaces
        : undefined,
    select,
    reload,
  };
}
