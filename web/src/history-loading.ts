import type { Chat } from "./types";

function afterPaint(signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = () => {
      signal.removeEventListener("abort", cancel);
      resolve();
    };
    const frame = requestAnimationFrame(() => {
      timer = setTimeout(finish, 0);
    });
    const cancel = () => {
      cancelAnimationFrame(frame);
      clearTimeout(timer);
      finish();
    };
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) cancel();
  });
}

// Yield after each committed page, so history hydration cannot block first paint
// or monopolize the main thread when the cache/network responds immediately.
export async function fetchOlderHistory(
  chat: Chat,
  fetchPage: (path: string, signal: AbortSignal) => Promise<Chat>,
  signal: AbortSignal,
  yieldToBrowser = afterPaint,
): Promise<Chat | null> {
  const before = chat.history?.before;
  if (
    !before ||
    !chat.history?.has_more ||
    chat.history.error ||
    signal.aborted
  )
    return null;
  await yieldToBrowser(signal);
  if (signal.aborted) return null;
  const page = await fetchPage(
    "/chats/" + chat.id + "/history?before=" + encodeURIComponent(before),
    signal,
  );
  if (signal.aborted) return null;
  if (
    page.id !== chat.id ||
    (page.history?.has_more &&
      (!page.history.before || page.history.before === before))
  ) {
    throw new Error("Invalid history cursor");
  }
  return page;
}

export type HistoryAnchor = { id: string; height: number; top: number };

export function restoreHistoryScroll(
  element: { scrollTop: number; scrollHeight: number },
  following: boolean,
  anchor: HistoryAnchor | null,
) {
  if (following) element.scrollTop = element.scrollHeight;
  else if (anchor)
    element.scrollTop = anchor.top + element.scrollHeight - anchor.height;
}
