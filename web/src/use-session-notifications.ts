import { useCallback, useEffect, useRef, useState } from "react";
import {
  desktopViewedChat,
  isLocalDesktopChat,
  subscribeDesktopServerSelection,
  subscribeDesktopSessionNotifications,
  subscribeDesktopSessionView,
  takeDesktopSessionNotifications,
  type SessionNotification,
  type SessionNotificationKind,
} from "./runtime";

export type SessionNotifications = Record<string, SessionNotificationKind>;

export function receiveSessionNotifications(
  previous: SessionNotifications,
  pending: SessionNotification[],
  user: string,
  viewedChat = "",
): SessionNotifications {
  const next = { ...previous };
  for (const notification of pending) {
    if (notification.user !== user) continue;
    if (notification.chat === viewedChat) delete next[notification.chat];
    else next[notification.chat] = notification.kind;
  }
  return next;
}

export function useSessionNotifications(
  user: string | undefined,
  chat: string,
) {
  const native = isLocalDesktopChat();
  const activeChat = useRef(chat);
  activeChat.current = chat;
  const [state, setState] = useState<{
    user?: string;
    notifications: SessionNotifications;
  }>({ notifications: {} });
  useEffect(() => {
    setState({ user, notifications: {} });
    if (!user || !native) return;
    const receive = () => {
      const pending = takeDesktopSessionNotifications();
      if (!pending.length) return;
      setState((previous) => ({
        user,
        notifications: receiveSessionNotifications(
          previous.user === user ? previous.notifications : {},
          pending,
          user,
          desktopViewedChat(),
        ),
      }));
    };
    const unsubscribe = subscribeDesktopSessionNotifications(receive);
    receive();
    return unsubscribe;
  }, [user, native]);
  const dismiss = useCallback((chat: string) => {
    setState((previous) => {
      if (!(chat in previous.notifications)) return previous;
      const notifications = { ...previous.notifications };
      delete notifications[chat];
      return { ...previous, notifications };
    });
  }, []);
  const receive = useCallback(
    (notification: SessionNotification) => {
      if (!user || isLocalDesktopChat()) return;
      const viewedChat =
        document.visibilityState === "visible" && document.hasFocus()
          ? activeChat.current
          : "";
      setState((previous) => ({
        user,
        notifications: receiveSessionNotifications(
          previous.user === user ? previous.notifications : {},
          [notification],
          user,
          viewedChat,
        ),
      }));
    },
    [user],
  );
  useEffect(() => {
    if (!user) return;
    const read = () => {
      if (
        native
          ? desktopViewedChat() === chat
          : document.visibilityState === "visible" && document.hasFocus()
      )
        dismiss(chat);
    };
    read();
    if (native) return subscribeDesktopSessionView(read);
    const unsubscribe = subscribeDesktopServerSelection(read);
    window.addEventListener("focus", read);
    document.addEventListener("visibilitychange", read);
    return () => {
      unsubscribe();
      window.removeEventListener("focus", read);
      document.removeEventListener("visibilitychange", read);
    };
  }, [user, chat, dismiss, native]);
  return {
    notifications: state.user === user ? state.notifications : {},
    dismiss,
    receive,
  };
}
