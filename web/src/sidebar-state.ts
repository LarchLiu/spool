import type { Chat, Project } from "./types";

export const INITIAL_CHAT_LIMIT = 5;

export function sortedSidebarChats(chats: Chat[]): Chat[] {
  return [...chats].sort((a, b) => b.updated_at - a.updated_at);
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
