import type { UserProfile } from "./user-profiles";

export type DirectMessage = {
  id: string;
  request_id: string;
  from: string;
  to: string;
  text: string;
  sent_at: number;
  group?: boolean;
};

export type PeopleChatGroup = {
  id: string;
  name: string;
  members: string[];
  created_by: string;
};

export type DirectMessageErrorCode =
  | "offline"
  | "invalid_message"
  | "rate_limited"
  | "unavailable"
  | "unknown"
  | "invalid_group"
  | "forbidden"
  | "group_limit";

export class DirectMessageError extends Error {
  constructor(public readonly code: DirectMessageErrorCode) {
    super(code);
  }
}

export const directMessageLimit = 2000;
export const inboxMessageLimit = 300;
export const onlineChatRoom = "online-room";
export const isPeopleGroup = (id: string) =>
  id === onlineChatRoom || id.startsWith("group-");

export function messagePeer(message: DirectMessage, user: string) {
  return message.group || message.to === onlineChatRoom
    ? message.to
    : message.from === user
      ? message.to
      : message.from;
}

export type DirectInbox = {
  active: string;
  messages: DirectMessage[];
  unread: Record<string, number>;
  people: UserProfile[];
  groups: PeopleChatGroup[];
};

export function createDirectInbox() {
  const empty = (): DirectInbox => ({
    active: "",
    messages: [],
    unread: {},
    people: [],
    groups: [],
  });
  let state = empty();
  const listeners = new Set<() => void>();
  const update = (next: DirectInbox) => {
    state = next;
    listeners.forEach((listener) => listener());
  };
  const remember = (people: UserProfile[], ids: Set<string>) => {
    const known = new Map(
      state.people.filter((p) => ids.has(p.id)).map((p) => [p.id, p]),
    );
    for (const person of people)
      if (ids.has(person.id)) known.set(person.id, person);
    return [...known.values()];
  };
  const contactIDs = () =>
    new Set([
      state.active,
      ...state.messages.flatMap((message) => [message.from, message.to]),
    ]);
  return {
    snapshot: () => state,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    open: (id: string, people: UserProfile[]) => {
      const ids = contactIDs();
      ids.add(id);
      update({
        ...state,
        active: id,
        unread: { ...state.unread, [id]: 0 },
        people: remember(people, ids),
      });
    },
    close: () => update({ ...state, active: "" }),
    groups: (groups: PeopleChatGroup[]) => update({ ...state, groups }),
    read: () => {
      if (state.active && state.unread[state.active])
        update({ ...state, unread: { ...state.unread, [state.active]: 0 } });
    },
    profiles: (people: UserProfile[]) => {
      const next = remember(people, contactIDs());
      if (
        next.length !== state.people.length ||
        next.some((person, i) => person !== state.people[i])
      )
        update({ ...state, people: next });
    },
    receive: (
      message: DirectMessage,
      user: string,
      visible: boolean,
      people: UserProfile[],
    ) => {
      if (
        (!message.group &&
          message.to !== onlineChatRoom &&
          message.from !== user &&
          message.to !== user) ||
        state.messages.some((old) => old.id === message.id)
      )
        return;
      const peer = messagePeer(message, user);
      const messages = [...state.messages, message].slice(-inboxMessageLimit);
      const ids = new Set([
        state.active,
        ...messages.flatMap((item) => [item.from, item.to]),
      ]);
      const unread = Object.fromEntries(
        Object.entries(state.unread).filter(([id]) => ids.has(id)),
      );
      if (message.from !== user && (state.active !== peer || !visible))
        unread[peer] = Math.min(99, (unread[peer] || 0) + 1);
      update({ ...state, messages, unread, people: remember(people, ids) });
    },
    reset: () => update(empty()),
  };
}
