export type ConnectionMode = "direct" | "tailscale" | "relay";
export type DesktopServerProfile = {
  id: string;
  name: string;
  server_name: string;
  address: string;
  direct: string;
  tailscale: string;
  relay: string;
  mode: ConnectionMode;
  enabled: boolean;
  automatic: boolean;
  federation_relay?: boolean;
};
export type ServerDialogSnapshot = {
  server: DesktopServerProfile;
  status: string;
  selected: boolean;
  active: boolean;
};

export function serverDialogInitialSnapshot():
  ServerDialogSnapshot | undefined {
  return (
    globalThis as typeof globalThis & {
      spoolServerDialog?: ServerDialogSnapshot;
    }
  ).spoolServerDialog;
}

export function serverDialogCall<T>(
  method:
    | "Snapshot"
    | "Ready"
    | "Save"
    | "Disconnect"
    | "Delete"
    | "Close"
    | "TailscaleLogin"
    | "PairRelay",
  ...args: unknown[]
): Promise<T> {
  const runtime = (
    globalThis as typeof globalThis & {
      mygo?: { call: <T>(method: string, ...args: unknown[]) => Promise<T> };
    }
  ).mygo;
  if (!runtime?.call)
    return Promise.reject(
      new Error(
        "Desktop settings are only available in the Spool desktop app.",
      ),
    );
  return runtime.call<T>(`DesktopServers.${method}`, ...args);
}
