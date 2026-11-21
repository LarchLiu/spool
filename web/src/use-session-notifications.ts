import { useCallback, useEffect, useRef, useState } from "react";
import {
  desktopViewedChat,
  isDesktopChat,
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
  const activeChat = useRef(chat);
  activeChat.current = chat;
  const [state, setState] = useState<{
    user?: string;
    notifications: SessionNotifications;
  }>({ notifications: {} });
  useEffect(() => {
    setState({ user, notifications: {} });
    if (!user) return;
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
  }, [user]);
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
      if (!user || isDesktopChat()) return;
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
        isDesktopChat()
          ? desktopViewedChat() === chat
          : document.visibilityState === "visible" && document.hasFocus()
      )
        dismiss(chat);
    };
    read();
    if (isDesktopChat()) return subscribeDesktopSessionView(read);
    window.addEventListener("focus", read);
    document.addEventListener("visibilitychange", read);
    return () => {
      window.removeEventListener("focus", read);
      document.removeEventListener("visibilitychange", read);
    };
  }, [user, chat, dismiss]);
  return {
    notifications: state.user === user ? state.notifications : {},
    dismiss,
    receive,
  };
}
