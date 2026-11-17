import { type SidebarPreferences, type SidebarSection } from "./sidebar-state";

export type SidebarSnapshot = { pinned: string[]; revision: number };
export type SidebarChange =
  { chat_id: string; pinned: boolean } | { initial: { pinned: string[] } };

function apply(value: SidebarSnapshot, change: SidebarChange): SidebarSnapshot {
  if ("initial" in change)
    return value.revision === 0 ? { ...change.initial, revision: 0 } : value;
  if (change.pinned && value.pinned.includes(change.chat_id)) return value;
  return {
    ...value,
    pinned: change.pinned
      ? [change.chat_id, ...value.pinned.filter((id) => id !== change.chat_id)]
      : value.pinned.filter((id) => id !== change.chat_id),
  };
}

// Rebase unsent operations over live snapshots. A delayed response cannot undo
// a newer revision, and each device sends explicit field changes, not a blob.
export function createSidebarSync(
  initial: SidebarPreferences,
  write: (
    change: SidebarChange,
    signal: AbortSignal,
  ) => Promise<SidebarSnapshot>,
  failed: (error: unknown) => void,
) {
  let base: SidebarSnapshot = { pinned: initial.pinned, revision: -1 };
  let collapsed = initial.collapsed;
  let value = { ...base, collapsed };
  let ready = false,
    active = false,
    busy = false,
    generation = 0;
  let controller = new AbortController();
  const pending: SidebarChange[] = [];
  const listeners = new Set<() => void>();
  function emit() {
    value = { ...pending.reduce(apply, base), collapsed };
    for (const listener of listeners) listener();
  }
  function accept(snapshot: SidebarSnapshot | undefined) {
    if (
      !snapshot ||
      !Number.isSafeInteger(snapshot.revision) ||
      snapshot.revision < 0
    )
      return;
    if (!ready) {
      ready = true;
      if (snapshot.revision === 0 && initial.pinned.length)
        pending.unshift({ initial: { pinned: initial.pinned } });
    }
    if (snapshot.revision >= base.revision) base = snapshot;
    emit();
    pump();
  }
  function pump() {
    if (!active || !ready || busy || !pending.length) return;
    busy = true;
    const change = pending[0],
      token = generation;
    void write(change, controller.signal)
      .then((snapshot) => {
        if (token !== generation) return;
        pending.shift();
        if (snapshot.revision >= base.revision) base = snapshot;
      })
      .catch((error) => {
        if (token !== generation) return;
        pending.shift();
        failed(error);
      })
      .finally(() => {
        if (token !== generation) return;
        busy = false;
        emit();
        pump();
      });
  }
  function change(operation: SidebarChange) {
    pending.push(operation);
    emit();
    pump();
  }
  return {
    snapshot: () => value,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    accept,
    start() {
      active = true;
      controller = new AbortController();
      pump();
    },
    stop() {
      active = false;
      generation++;
      controller.abort();
      busy = false;
    },
    togglePin(chat: string) {
      if (!value.pinned.includes(chat))
        collapsed = { ...collapsed, pinned: false };
      change({ chat_id: chat, pinned: !value.pinned.includes(chat) });
    },
    toggleSection(section: SidebarSection) {
      collapsed = { ...collapsed, [section]: !collapsed[section] };
      emit();
    },
  };
}
