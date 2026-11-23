import { useEffect, useRef, useState } from "react";
import { appURL } from "./app-path";
import { isDesktopChat } from "./runtime";
import type { Translator } from "./i18n";
import { getCSRF } from "./api";

export const remoteDesktopWindowPath = "/__desktop/remote-desktop";
export const remoteDesktopClosePath = "/__desktop/remote-desktop-close";
export const remoteDesktopConnectedPath = "/__desktop/remote-desktop-connected";
const stateEvent = "spool:remote-desktop";

export function disconnectRemoteDesktop(token: string) {
  return fetch(
    appURL(`/api/desktop/disconnect?token=${encodeURIComponent(token)}`),
    {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "X-CSRF-Token": getCSRF(),
        "Content-Type": "application/json",
      },
      body: "{}",
      keepalive: true,
    },
  ).catch(() => {});
}

export function createRemoteDesktopWindow(
  surface: Window,
  onConnected: (connected: boolean) => void,
  onBlocked: () => void,
  serverName: () => string = () => "",
) {
  let viewer: Window | null = null;
  let token: string | undefined;
  let polling: ReturnType<typeof setInterval> | undefined;
  const native = isDesktopChat(surface);
  const reset = () => {
    clearInterval(polling);
    viewer = null;
    if (token) {
      void disconnectRemoteDesktop(token);
      token = undefined;
    }
    onConnected(false);
  };
  const message = (event: MessageEvent) => {
    if (
      event.origin === surface.location.origin &&
      event.source === viewer &&
      viewer &&
      event.data?.type === "spool:remote-desktop-connected" &&
      typeof event.data.token === "string" &&
      event.data.token
    ) {
      token = event.data.token;
      onConnected(true);
    }
  };
  const nativeState = (event: Event) => {
    const state = (event as CustomEvent).detail;
    if (!native || typeof state?.connected !== "boolean") return;
    if (!state.connected) reset();
    else if (typeof state.token === "string" && state.token) {
      token = state.token;
      onConnected(true);
    }
  };
  const close = () => {
    if (native) surface.location.assign(remoteDesktopClosePath);
    else viewer?.close();
    reset();
  };
  surface.addEventListener("message", message);
  surface.addEventListener(stateEvent, nativeState);
  // Navigation does not unmount React. End the owned popup, while keeping
  // this controller usable if the chat returns from the back/forward cache.
  if (!native) surface.addEventListener("pagehide", close);
  return {
    open() {
      if (native) {
        surface.location.assign(remoteDesktopWindowPath);
        return;
      }
      if (viewer && !viewer.closed) {
        viewer.focus();
        return;
      }
      reset();
      // Open synchronously during the click so browser popup rules allow it.
      viewer = surface.open(
        appURL("/remote-desktop.html") +
          "?" +
          new URLSearchParams({ server: serverName() }),
        "_blank",
        "popup,width=1280,height=850",
      );
      if (!viewer) {
        onBlocked();
        return;
      }
      polling = setInterval(() => {
        if (viewer?.closed) reset();
      }, 500);
    },
    dispose() {
      surface.removeEventListener("message", message);
      surface.removeEventListener(stateEvent, nativeState);
      surface.removeEventListener("pagehide", close);
      close();
    },
  };
}

export function useRemoteDesktopWindow(
  account: string | undefined,
  t: Translator,
  onError: (error: Error) => void,
  serverName: string,
) {
  const [connected, setConnected] = useState(false);
  const controller =
    useRef<ReturnType<typeof createRemoteDesktopWindow>>(undefined);
  const blocked = useRef(() => {});
  const name = useRef(serverName);
  name.current = serverName;
  blocked.current = () =>
    onError(
      new Error(t("Allow pop-up windows to connect to the remote desktop.")),
    );
  useEffect(() => {
    if (!account) return;
    const current = createRemoteDesktopWindow(
      window,
      setConnected,
      () => blocked.current(),
      () => name.current,
    );
    controller.current = current;
    return () => {
      controller.current = undefined;
      current.dispose();
    };
  }, [account]);
  return { connected, open: () => controller.current?.open() };
}

export function notifyRemoteDesktopConnected(token: string) {
  if (isDesktopChat())
    window.location.assign(
      remoteDesktopConnectedPath + "?" + new URLSearchParams({ token }),
    );
  else
    window.opener?.postMessage(
      { type: "spool:remote-desktop-connected", token },
      window.location.origin,
    );
}

export function closeRemoteDesktopWindow() {
  if (isDesktopChat()) window.location.assign(remoteDesktopClosePath);
  else window.close();
}
