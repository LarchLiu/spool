import type {
  Chat,
  ChatMetadata,
  Transcript,
  TranscriptPart,
  Turn,
} from "./types";

export function canControlChat(chat: Chat | null, userID: string): boolean {
  if (!chat) return true;
  if (chat.automation_chat) return !!chat.collaborator_ids?.includes(userID);
  return chat.user_id == null || chat.user_id === userID;
}

export function reconcileMetadata(
  previous: Chat | null,
  next: ChatMetadata,
): Chat | null {
  if (previous?.id !== next.id || previous.updated_at > next.updated_at)
    return previous;
  return {
    ...previous,
    updated_at: next.updated_at,
    automation_chat: next.automation_chat ?? false,
    collaborator_ids: next.collaborator_ids ?? [],
    artifacts: next.artifacts ?? [],
  };
}

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
  if (
    previous?.id === next.id &&
    previous.status !== "running" &&
    next.status === "running"
  ) {
    return previous;
  }
  if (
    previous?.id === next.id &&
    previous.part_end > previous.part_start &&
    next.part_end === next.part_start
  ) {
    // A native session can lag the live stream. Keep the live ranges
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
  if (previous.history || next.history) return reconcilePaged(previous, next);
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

// Page-local part offsets must be rebased by turn identity, never compared as
// though REST pages and live snapshots shared a single transcript prefix.
function reconcilePaged(previous: Chat, next: Chat, older = false): Chat {
  const byID = new Map<string, { turn: Turn; parts: TranscriptPart[] }>();
  const aliases = new Map<string, string>();
  for (const turn of previous.turns) {
    byID.set(turn.id, {
      turn,
      parts: previous.transcript.parts.slice(turn.part_start, turn.part_end),
    });
    if (turn.native_turn_id) aliases.set(turn.native_turn_id, turn.id);
  }
  const renamed = new Map<string, string>();
  let newServerTurn = false;
  for (const turn of next.turns) {
    const oldID = byID.has(turn.id)
      ? turn.id
      : turn.native_turn_id
        ? aliases.get(turn.native_turn_id)
        : undefined;
    const old = oldID ? byID.get(oldID) : undefined;
    if (!old && !turn.id.startsWith("pending-")) newServerTurn = true;
    if (oldID && oldID !== turn.id) {
      byID.delete(oldID);
      renamed.set(oldID, turn.id);
    }
    const incoming = next.transcript.parts.slice(
      turn.part_start,
      turn.part_end,
    );
    const parts = old
      ? mergeTranscript({ parts: old.parts }, { parts: incoming }).parts
      : incoming;
    const inputs = incoming.length
      ? turn.input_end - turn.part_start
      : old
        ? old.turn.input_end - old.turn.part_start
        : 0;
    const merged = mergeTurn(
      old ? { ...old.turn, id: turn.id } : undefined,
      turn,
    );
    byID.set(turn.id, {
      turn: { ...merged, part_start: 0, input_end: inputs },
      parts,
    });
  }
  const oldIDs = previous.turns.map((turn) => renamed.get(turn.id) || turn.id);
  const incomingIDs = next.turns.map((turn) => turn.id);
  const incoming = new Set(incomingIDs);
  const missing = oldIDs.filter((id) => !incoming.has(id));
  // File/page order is authoritative. Converted sessions may have zero or
  // repeated timestamps, and clocks can move backwards between real turns.
  const order = older
    ? [...incomingIDs, ...missing]
    : [
        ...missing.filter((id) => !id.startsWith("pending-")),
        ...incomingIDs,
        ...missing.filter((id) => id.startsWith("pending-") && !newServerTurn),
      ];
  const parts: TranscriptPart[] = [];
  const turns = [...new Set(order)].map((id) => {
    const entry = byID.get(id)!;
    const start = parts.length;
    const inputs = Math.min(
      entry.parts.length,
      entry.turn.input_end - entry.turn.part_start,
    );
    parts.push(...entry.parts);
    return {
      ...entry.turn,
      part_start: start,
      input_end: start + inputs,
      part_end: parts.length,
    };
  });
  const history =
    older || turns[0]?.id === next.turns[0]?.id
      ? next.history
      : previous.history;
  const metadata = older ? previous : next;
  return {
    ...metadata,
    history,
    turns,
    transcript: { ...metadata.transcript, parts },
    status: turns.at(-1)?.status || metadata.status,
  };
}

export function prependHistory(previous: Chat, page: Chat): Chat {
  if (previous.id !== page.id) return previous;
  return reconcilePaged(previous, page, true);
}
