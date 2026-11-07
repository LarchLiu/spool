import type { SessionWorkspace, SessionWorkspaceView } from "./types";

export type WorkspaceSelection = {
  latest: SessionWorkspaceView;
  workspace: SessionWorkspace;
  manualPath?: string;
};

export function receiveSessionWorkspace(
  previous: WorkspaceSelection | undefined,
  latest: SessionWorkspaceView,
): WorkspaceSelection {
  const changed =
    previous &&
    (previous.latest.path !== latest.path ||
      (latest.turn_id && previous.latest.turn_id !== latest.turn_id));
  const manualPath = changed ? undefined : previous?.manualPath;
  const manual = latest.workspaces?.find((item) => item.path === manualPath);
  return { latest, workspace: manual || latest, manualPath: manual?.path };
}

export function selectSessionWorkspace(
  state: WorkspaceSelection,
  path: string,
): WorkspaceSelection {
  const workspace = state.latest.workspaces?.find((item) => item.path === path);
  return workspace?.available
    ? { ...state, workspace, manualPath: path }
    : state;
}

export function workspaceDirectoryName(path: string) {
  return (
    path.replaceAll("\\", "/").replace(/\/+$/, "").split("/").pop() || path
  );
}
