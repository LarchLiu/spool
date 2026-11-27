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

type ServerDialogMethod =
  | "Snapshot"
  | "Ready"
  | "Save"
  | "Disconnect"
  | "Delete"
  | "Close"
  | "TailscaleLogin"
  | "PairRelay";

export function serverDialogCall<T>(
  method: ServerDialogMethod,
  ...args: unknown[]
): Promise<T> {
  return serverDialogCallWith<T>({}, method, ...args);
}

export function serverDialogCallWith<T>(
  options: { signal?: AbortSignal },
  method: ServerDialogMethod,
  ...args: unknown[]
): Promise<T> {
  if (options.signal?.aborted) return Promise.reject(options.signal.reason);
  const runtime = (
    globalThis as typeof globalThis & {
      mygo?: {
        call: <T>(method: string, ...args: unknown[]) => Promise<T>;
        callWith?: <T>(
          method: string,
          args: unknown[],
          options: { signal?: AbortSignal },
        ) => Promise<T>;
      };
    }
  ).mygo;
  if (!runtime?.call)
    return Promise.reject(
      new Error(
        "Desktop settings are only available in the Spool desktop app.",
      ),
    );
  if (options.signal) {
    if (!runtime.callWith)
      return Promise.reject(
        new Error("This desktop runtime does not support canceling calls."),
      );
    return runtime.callWith<T>(`DesktopServers.${method}`, args, options);
  }
  return runtime.call<T>(`DesktopServers.${method}`, ...args);
}
