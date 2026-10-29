import type { Agent, Chat, Online, Project } from "./types";
import type { ProfileSnapshot } from "./user-profiles";
import {
  DirectMessageError,
  directMessageLimit,
  isPeopleGroup,
  type DirectMessage,
  type DirectMessageErrorCode,
  type PeopleChatGroup,
} from "./direct-messages.js";

export type Navigation = { projects: Project[]; chats: Chat[] };

export function mergeNavigationChats(
  previous: Chat[],
  incoming: Chat[],
): Chat[] {
  const byID = new Map(previous.map((item) => [item.id, item]));
  // Snapshot membership is authoritative, even if a removed chat has a newer
  // SSE revision. Keep newer metadata only for IDs still authorized/listed.
  return incoming.map((next) => {
    const old = byID.get(next.id);
    return old && old.updated_at > next.updated_at
      ? { ...old, can_control: next.can_control }
      : next;
  });
}

export function subscribeWorkspaceEvents(
  handlers: {
    navigation: (value: Navigation) => void;
    agents: (value: Agent[]) => void;
    presence: (value: Online[]) => void;
    profiles: (value: ProfileSnapshot) => void;
    connected: (value: boolean) => void;
    expired: () => void;
    directMessage?: (value: DirectMessage) => void;
    groups?: (value: PeopleChatGroup[]) => void;
  },
  transport: {
    open: () => WebSocket;
    csrf: () => string;
    visible: () => boolean;
    checkAuth: (signal: AbortSignal) => Promise<unknown>;
  },
) {
  const controller = new AbortController();
  let socket: WebSocket | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let watchdog: ReturnType<typeof setTimeout> | undefined;
  let attempts = 0;
  let ready = false;
  let lastMessage = 0;
  let checkingAuth = false;
  let sequence = 0;
  const pending = new Map<
    string,
    {
      resolve: (message: DirectMessage | PeopleChatGroup) => void;
      reject: (error: DirectMessageError) => void;
      timer: ReturnType<typeof setTimeout>;
    }
  >();
  const rejectPending = () => {
    for (const request of pending.values()) {
      clearTimeout(request.timer);
      request.reject(new DirectMessageError("unknown"));
    }
    pending.clear();
  };

  const clearTimers = () => {
    clearTimeout(retry);
    clearTimeout(watchdog);
    retry = watchdog = undefined;
  };
  const send = (type: string) => {
    if (socket?.readyState === 1)
      socket.send(JSON.stringify({ type, visible: transport.visible() }));
  };
  const lost = (current?: WebSocket) => {
    if (controller.signal.aborted || (current && socket !== current)) return;
    clearTimers();
    socket = undefined;
    current?.close();
    ready = false;
    rejectPending();
    handlers.connected(false);
    if (!checkingAuth) {
      checkingAuth = true;
      void transport
        .checkAuth(controller.signal)
        .catch(() => {})
        .finally(() => {
          checkingAuth = false;
        });
    }
    const delay = Math.min(30000, 500 * 2 ** Math.min(attempts++, 6));
    retry = setTimeout(connect, delay + Math.random() * delay * 0.2);
  };
  const armWatchdog = (current: WebSocket) => {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => lost(current), 45000);
  };
  function connect() {
    if (controller.signal.aborted) return;
    clearTimers();
    let current: WebSocket;
    try {
      current = transport.open();
    } catch {
      lost();
      return;
    }
    socket = current;
    armWatchdog(current);
    current.onopen = () => {
      if (socket !== current || controller.signal.aborted) return;
      current.send(
        JSON.stringify({
          type: "hello",
          csrf: transport.csrf(),
          visible: transport.visible(),
        }),
      );
    };
    current.onmessage = (event) => {
      if (socket !== current || controller.signal.aborted) return;
      let message: { type: string; data: unknown };
      try {
        message = JSON.parse(event.data);
      } catch {
        lost(current);
        return;
      }
      lastMessage = Date.now();
      armWatchdog(current);
      switch (message.type) {
        case "navigation":
          handlers.navigation(message.data as Navigation);
          break;
        case "agents":
          handlers.agents(message.data as Agent[]);
          break;
        case "presence":
          handlers.presence(message.data as Online[]);
          break;
        case "profiles":
          handlers.profiles(message.data as ProfileSnapshot);
          break;
        case "direct_message":
        case "group_message": {
          const value = message.data as DirectMessage;
          const request = pending.get(value.request_id);
          if (request) {
            clearTimeout(request.timer);
            pending.delete(value.request_id);
            request.resolve(value);
          }
          handlers.directMessage?.(value);
          break;
        }
        case "direct_message_error": {
          const value = message.data as {
            request_id: string;
            code: DirectMessageErrorCode;
          };
          const request = pending.get(value.request_id);
          if (request) {
            clearTimeout(request.timer);
            pending.delete(value.request_id);
            request.reject(new DirectMessageError(value.code));
          }
          break;
        }
        case "chat_groups":
          handlers.groups?.(message.data as PeopleChatGroup[]);
          break;
        case "group_result": {
          const value = message.data as {
            request_id: string;
            group: PeopleChatGroup;
          };
          const request = pending.get(value.request_id);
          if (request) {
            clearTimeout(request.timer);
            pending.delete(value.request_id);
            request.resolve(value.group);
          }
          break;
        }
        case "ping":
          send("pong");
          break;
        case "ready":
          ready = true;
          attempts = 0;
          handlers.connected(true);
          break;
      }
    };
    current.onerror = () => lost(current);
    current.onclose = (event) => {
      if (socket !== current || controller.signal.aborted) return;
      if (event.code === 1008) {
        close();
        handlers.expired();
      } else lost(current);
    };
  }
  function close() {
    controller.abort();
    clearTimers();
    ready = false;
    rejectPending();
    const current = socket;
    socket = undefined;
    current?.close();
  }
  connect();
  const request = <T extends DirectMessage | PeopleChatGroup>(
    type: string,
    fields: object,
  ): Promise<T> => {
    if (!ready || socket?.readyState !== 1 || controller.signal.aborted)
      return Promise.reject(new DirectMessageError("unavailable"));
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    const requestID =
      Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
        "",
      ) +
      "-" +
      sequence++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(requestID);
        reject(new DirectMessageError("unknown"));
      }, 10000);
      pending.set(requestID, {
        resolve: (value) => resolve(value as T),
        reject,
        timer,
      });
      try {
        socket!.send(
          JSON.stringify({
            type,
            request_id: requestID,
            ...fields,
            visible: transport.visible(),
          }),
        );
      } catch {
        lost(socket);
      }
    });
  };
  return {
    close,
    sendMessage: (to: string, text: string): Promise<DirectMessage> => {
      if (!ready || socket?.readyState !== 1 || controller.signal.aborted)
        return Promise.reject(new DirectMessageError("unavailable"));
      if (!text.trim() || [...text].length > directMessageLimit)
        return Promise.reject(new DirectMessageError("invalid_message"));
      // Messages are never automatically replayed after reconnecting.
      return request<DirectMessage>(
        isPeopleGroup(to) ? "group_message" : "direct_message",
        { to, text },
      );
    },
    createGroup: (name: string, members: string[]) =>
      request<PeopleChatGroup>("group_create", { name, members }),
    inviteGroup: (to: string, members: string[]) =>
      request<PeopleChatGroup>("group_invite", { to, members }),
    visibility: () => send("visibility"),
    resume: () => {
      if (controller.signal.aborted) return;
      if (ready && Date.now() - lastMessage < 45000) send("refresh");
      else {
        const current = socket;
        socket = undefined;
        current?.close();
        ready = false;
        rejectPending();
        handlers.connected(false);
        connect();
      }
    },
  };
}
