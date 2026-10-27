import type { Chat, Transcript, TranscriptPart, Turn } from "./types";

export function optimisticChat(
  chat: Chat | null,
  projectID: string,
  turn: Turn,
  inputParts: TranscriptPart[] = [],
): Chat {
  const partStart = chat?.transcript.parts.length || 0;
  const nextTurn = {
    ...turn,
    part_start: partStart,
    input_end: partStart + inputParts.length,
    part_end: partStart + inputParts.length,
  };
  return {
    ...(chat || {
      id: turn.id,
      project_id: projectID,
      title: "New chat",
      updated_at: 0,
      transcript: { parts: [] },
    }),
    backend: nextTurn.backend || chat?.backend || "codex",
    model: nextTurn.requested_model,
    reasoning_effort: nextTurn.reasoning_effort,
    permission_mode: nextTurn.permission_mode,
    status: "running",
    transcript: {
      parts: [...(chat?.transcript.parts || []), ...inputParts],
    },
    turns: [...(chat?.turns || []), nextTurn],
  };
}

export function adoptCreatedChat(pending: Chat, created: Chat): Chat {
  return reconcile(
    { ...pending, id: created.id, updated_at: created.updated_at },
    {
      ...created,
      model: pending.model,
      reasoning_effort: pending.reasoning_effort,
      permission_mode: pending.permission_mode,
    },
  );
}

function mergeTranscript(previous: Transcript, next: Transcript): Transcript {
  // Metadata-only snapshots are expected while a turn is running. They must
  // not erase the live transcript when a status refresh arrives before the
  // provider's native session has been hydrated.
  if (previous.parts.length > 0 && next.parts.length === 0) return previous;
  if (previous.parts.length > next.parts.length) return previous;
  const parts = next.parts.slice();
  for (let i = 0; i < previous.parts.length; i++) {
    const oldPart = previous.parts[i];
    const newPart = parts[i];
    if (
      oldPart.role === newPart.role &&
      oldPart.kind === newPart.kind &&
      oldPart.name === newPart.name &&
      oldPart.model === newPart.model &&
      oldPart.text.length > newPart.text.length
    ) {
      parts[i] = oldPart;
    }
  }
  return { ...next, parts };
}

function mergeTurn(previous: Turn | undefined, next: Turn): Turn {
  if (previous?.id === next.id && previous.status !== "running" && next.status === "running") {
    return previous;
  }
  if (
    previous?.id === next.id &&
    previous.part_end > previous.part_start &&
    next.part_end === next.part_start
  ) {
    // SQLite intentionally omits transcript ranges. Keep the live ranges
    // while applying the newer lifecycle status and completion timestamp.
    return {
      ...next,
      part_start: previous.part_start,
      input_end: previous.input_end,
      part_end: previous.part_end,
    };
  }
  return next;
}

// REST replies and SSE snapshots can arrive out of order. Never let an older
// response erase a submitted message or put a completed turn back into Working.
export function reconcile(previous: Chat | null, next: Chat): Chat {
  if (previous?.id !== next.id) return next;
  if (previous.updated_at > next.updated_at) return previous;
  const oldByID = new Map(previous.turns.map((turn) => [turn.id, turn]));
  const hasNewServerTurn = next.turns.some(
    (turn) => !turn.id.startsWith("pending-") && !oldByID.has(turn.id),
  );
  const turns = next.turns.map((turn) => mergeTurn(oldByID.get(turn.id), turn));
  const seen = new Set(turns.map((turn) => turn.id));
  for (const turn of previous.turns) {
    if (seen.has(turn.id)) continue;
    if (turn.id.startsWith("pending-") && hasNewServerTurn) continue;
    turns.push(turn);
  }
  return {
    ...next,
    turns,
    transcript: mergeTranscript(previous.transcript, next.transcript),
    status: turns.at(-1)?.status || next.status,
  };
}
