import type { Chat, Project } from "./types";

export const INITIAL_CHAT_LIMIT = 5;

export type SidebarSection = "pinned" | "projects" | "recents";
export type SidebarPreferences = {
  pinned: string[];
  collapsed: Record<SidebarSection, boolean>;
};

export function parseSidebarPreferences(
  raw: string | null,
): SidebarPreferences {
  let value;
  try {
    value = JSON.parse(raw || "null");
  } catch {
    // Ignore malformed device storage.
  }
  return {
    pinned: Array.isArray(value?.pinned)
      ? [
          ...new Set<string>(
            value.pinned.filter(
              (id: unknown) => typeof id === "string" && id.length > 0,
            ),
          ),
        ]
      : [],
    collapsed: {
      pinned: value?.collapsed?.pinned === true,
      projects: value?.collapsed?.projects === true,
      recents: value?.collapsed?.recents === true,
    },
  };
}

export function pinnedSidebarChats(chats: Chat[], pinned: string[]): Chat[] {
  const available = new Map(chats.map((chat) => [chat.id, chat]));
  return pinned.flatMap((id) => {
    const chat = available.get(id);
    return chat ? [chat] : [];
  });
}

export function sortedSidebarChats(chats: Chat[]): Chat[] {
  return [...chats].sort((a, b) => b.updated_at - a.updated_at);
}

export function sidebarChatAge(timestamp: number, now: number): string | null {
  if (timestamp <= 0 || !Number.isFinite(timestamp) || !Number.isFinite(now))
    return null;
  const minutes = Math.max(0, Math.floor((now - timestamp) / 60_000));
  for (const [size, unit] of [
    [525_600, "y"],
    [43_200, "mo"],
    [1_440, "d"],
    [60, "h"],
    [1, "m"],
  ] as const) {
    if (minutes >= size) return `${Math.floor(minutes / size)}${unit}`;
  }
  return "now";
}

export function nextSidebarChatLimit(total: number, limit: number): number {
  return total > limit ? Math.min(limit + 10, total) : INITIAL_CHAT_LIMIT;
}

export function revealSidebarProject(
  projects: Project[],
  project: string,
  limit: number,
): number {
  return Math.max(limit, projects.findIndex((item) => item.id === project) + 1);
}

export function revealSidebarChat(
  chats: Chat[],
  chat: string,
  limit: number,
): number {
  return Math.max(limit, chats.findIndex((item) => item.id === chat) + 1);
}
