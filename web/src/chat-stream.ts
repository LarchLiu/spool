import type { Chat, ChatMetadata, Turn } from "./types";

export type ChatTurnEvent = {
  turn: Turn;
  turns?: Turn[];
  history?: Chat["history"];
  transcript: Chat["transcript"];
  status: string;
  title: string;
  updated_at: number;
  backend: "codex" | "claude";
  model?: string;
  reasoning_effort?: string;
  permission_mode?: string;
};

export function subscribeChatEvents(
  chatID: string,
  handlers: {
    snapshot: (chat: Chat) => void;
    metadata: (metadata: ChatMetadata) => void;
    turn: (turn: ChatTurnEvent) => void;
    connected: (connected: boolean) => void;
    error: (error: unknown) => void;
  },
  transport: {
    open: (path: string) => EventSource;
    snapshot: (signal: AbortSignal) => Promise<Chat>;
    checkAuth: (signal: AbortSignal) => Promise<unknown>;
  },
): () => void {
  const source = transport.open("/api/chats/" + chatID + "/events");
  const controller = new AbortController();
  let initialized = false;
  let fallback: AbortController | undefined;
  const listen = <T>(name: string, receive: (value: T) => void) => {
    source.addEventListener(name, (event) => {
      if (controller.signal.aborted) return;
      handlers.connected(true);
      receive(JSON.parse((event as MessageEvent).data) as T);
    });
  };
  listen<Chat>("snapshot", (chat) => {
    initialized = true;
    fallback?.abort();
    handlers.snapshot(chat);
  });
  listen<ChatMetadata>("metadata", handlers.metadata);
  listen<ChatTurnEvent>("turn", handlers.turn);
  source.onerror = () => {
    if (controller.signal.aborted) return;
    handlers.connected(false);
    if (initialized) {
      void transport.checkAuth(controller.signal).catch(() => {});
      return;
    }
    if (fallback) return;
    // EventSource hides HTTP errors. Fetch only on failed initialization, so
    // missing/inaccessible chats and expired logins still surface normally.
    const request = new AbortController();
    fallback = request;
    void transport
      .snapshot(request.signal)
      .then((chat) => {
        if (request.signal.aborted || controller.signal.aborted || initialized)
          return;
        initialized = true;
        handlers.snapshot(chat);
      })
      .catch((error) => {
        if (!request.signal.aborted && !controller.signal.aborted)
          handlers.error(error);
      })
      .finally(() => {
        if (fallback === request) fallback = undefined;
      });
  };
  return () => {
    controller.abort();
    fallback?.abort();
    source.close();
  };
}
