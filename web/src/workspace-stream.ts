import type { Agent, Chat, Online, Project } from "./types";

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
    return old && old.updated_at > next.updated_at ? old : next;
  });
}

export function subscribeWorkspaceEvents(
  handlers: {
    navigation: (value: Navigation) => void;
    agents: (value: Agent[]) => void;
    presence: (value: Online[]) => void;
    connected: (value: boolean) => void;
    expired: () => void;
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
    const current = socket;
    socket = undefined;
    current?.close();
  }
  connect();
  return {
    close,
    visibility: () => send("visibility"),
    resume: () => {
      if (controller.signal.aborted) return;
      if (ready && Date.now() - lastMessage < 45000) send("refresh");
      else {
        const current = socket;
        socket = undefined;
        current?.close();
        ready = false;
        handlers.connected(false);
        connect();
      }
    },
  };
}
