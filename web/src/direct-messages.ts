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
  groupNames: Record<string, string>;
  drafts: Record<string, { text: string }>;
  sending: Record<string, boolean>;
};

export function peopleConversationIDs(
  inbox: DirectInbox,
  user: string,
  online: string[],
) {
  return [
    ...new Set([
      onlineChatRoom,
      ...inbox.groups.map((group) => group.id),
      ...online,
      ...inbox.people.map((person) => person.id),
      ...inbox.messages.map((message) => messagePeer(message, user)),
      ...Object.keys(inbox.drafts),
      inbox.active,
    ]),
  ].filter((id) => id && id !== user);
}

export function createDirectInbox() {
  const empty = (): DirectInbox => ({
    active: "",
    messages: [],
    unread: {},
    people: [],
    groups: [],
    groupNames: {},
    drafts: {},
    sending: {},
  });
  let state = empty();
  let generation = 0;
  const listeners = new Set<() => void>();
  const contactIDs = (inbox = state) =>
    new Set([
      inbox.active,
      ...Object.keys(inbox.drafts),
      ...Object.keys(inbox.sending),
      ...inbox.messages.flatMap((message) => [message.from, message.to]),
    ]);
  const update = (next: DirectInbox) => {
    const ids = contactIDs(next);
    const groupIDs = new Set([...ids, ...next.groups.map((group) => group.id)]);
    state = {
      ...next,
      people: next.people.filter((person) => ids.has(person.id)),
      unread: Object.fromEntries(
        Object.entries(next.unread).filter(([id]) => ids.has(id)),
      ),
      // Remember labels, not membership. Only the live groups authorize sending.
      groupNames: Object.fromEntries(
        Object.entries(next.groupNames).filter(([id]) => groupIDs.has(id)),
      ),
    };
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
    groups: (groups: PeopleChatGroup[]) =>
      update({
        ...state,
        groups,
        groupNames: {
          ...state.groupNames,
          ...Object.fromEntries(groups.map((group) => [group.id, group.name])),
        },
      }),
    draft: (id: string, text: string) => {
      const drafts = { ...state.drafts };
      if (text) drafts[id] = { text };
      else delete drafts[id];
      update({ ...state, drafts });
    },
    send: async (
      id: string,
      text: string,
      send: (id: string, text: string) => Promise<DirectMessage>,
    ) => {
      if (state.sending[id]) throw new DirectMessageError("rate_limited");
      const draft = state.drafts[id];
      const started = generation;
      update({ ...state, sending: { ...state.sending, [id]: true } });
      try {
        const message = await send(id, text);
        // Acknowledgements belong to the original draft, even after switching
        // panels. Never clear a newer edit or a different signed-in session.
        if (
          generation === started &&
          draft?.text === text &&
          state.drafts[id] === draft
        ) {
          const drafts = { ...state.drafts };
          delete drafts[id];
          update({ ...state, drafts });
        }
        return message;
      } finally {
        if (generation === started) {
          const sending = { ...state.sending };
          delete sending[id];
          update({ ...state, sending });
        }
      }
    },
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
      const ids = contactIDs({ ...state, messages });
      const unread = Object.fromEntries(
        Object.entries(state.unread).filter(([id]) => ids.has(id)),
      );
      if (message.from !== user && (state.active !== peer || !visible))
        unread[peer] = Math.min(99, (unread[peer] || 0) + 1);
      update({ ...state, messages, unread, people: remember(people, ids) });
    },
    reset: () => {
      generation++;
      update(empty());
    },
  };
}
