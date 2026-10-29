import type { AvatarIdentity } from "./avatar";

export type UserProfile = Pick<AvatarIdentity, "id" | "nickname" | "avatar">;
export type ProfileSnapshot = { revision: number; people: UserProfile[] };

export const emptyProfiles: ProfileSnapshot = { revision: -1, people: [] };

export function mergeProfiles(
  previous: ProfileSnapshot,
  incoming: ProfileSnapshot,
): ProfileSnapshot {
  return incoming.revision < previous.revision ? previous : incoming;
}

export function profileLookup(snapshot: ProfileSnapshot) {
  const people = new Map(snapshot.people.map((person) => [person.id, person]));
  return (id: string): AvatarIdentity =>
    id === "spool-bot"
      ? { id, nickname: "Spool Bot", bot: true }
      : people.get(id) || { id, nickname: id };
}

export function createProfileCache() {
  let snapshot = emptyProfiles;
  let epoch = 0;
  let pending: { request: Promise<void>; signal: AbortSignal } | undefined;
  const listeners = new Set<() => void>();
  const emit = () => listeners.forEach((listener) => listener());
  const accept = (incoming: ProfileSnapshot) => {
    const next = mergeProfiles(snapshot, incoming);
    if (next === snapshot) return;
    snapshot = next;
    epoch++;
    emit();
  };
  return {
    snapshot: () => snapshot,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    accept,
    acceptSaved: (incoming: ProfileSnapshot, owner: string) => {
      // A save acknowledgement may predate a permissions/presence push at the
      // same revision. Refresh values, but never restore its older directory.
      const updates = new Map(
        incoming.people.map((person) => [person.id, person]),
      );
      const people = snapshot.people.map(
        (person) => updates.get(person.id) || person,
      );
      const own = updates.get(owner);
      if (own && !people.some((person) => person.id === owner))
        people.push(own);
      accept({ revision: incoming.revision, people });
    },
    reset: () => {
      epoch++;
      pending = undefined;
      snapshot = emptyProfiles;
      emit();
    },
    load: (fetch: () => Promise<ProfileSnapshot>, signal: AbortSignal) => {
      if (signal.aborted) return Promise.resolve();
      if (pending && !pending.signal.aborted) return pending.request;
      const version = epoch;
      const request = fetch()
        .then((value) => {
          // A push also changes directory membership without changing profile
          // revisions. Fence delayed HTTP replies as well as comparing revisions.
          if (!signal.aborted && version === epoch) accept(value);
        })
        .finally(() => {
          if (pending?.request === request) pending = undefined;
        });
      pending = { request, signal };
      return request;
    },
  };
}
