import type { Chat, Turn } from "./types";

export function runningTurn(chat: Chat | null): Turn | undefined {
  const turn = chat?.turns.at(-1);
  return chat?.status === "running" && turn?.status === "running"
    ? turn
    : undefined;
}

export async function stopReply(
  chat: Chat,
  submission: Promise<Chat | null> | undefined,
  stop: (chatID: string, turnID: string) => Promise<Chat>,
): Promise<Chat | null> {
  // A click during upload/first-chat creation must wait for the server turn ID.
  // Aborting the POST alone would leave the accepted agent task running.
  const accepted = submission ? await submission : chat;
  if (!accepted) return null;
  const turn = runningTurn(accepted);
  if (!turn) return accepted;
  if (turn.id.startsWith("pending-")) return null;
  return stop(accepted.id, turn.id);
}
