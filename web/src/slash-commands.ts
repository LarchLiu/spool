export type GoalCommand = {
  action: "get" | "clear" | "set";
  objective?: string;
};

export function parseGoalCommand(message: string): GoalCommand | null {
  const match = /^\/goal(?:\s+([\s\S]*))?$/.exec(message.trim());
  if (!match) return null;
  const argument = (match[1] || "").trim();
  if (!argument) return { action: "get" };
  if (["clear", "stop", "off", "reset", "none", "cancel"].includes(argument))
    return { action: "clear" };
  return { action: "set", objective: argument };
}

export function goalSuggestion(message: string): boolean {
  return /^\/(?:g(?:o(?:a(?:l)?)?)?)?$/.test(message);
}
import type { AgentCommand } from "./types";

export function parseSlashCommand(message: string) {
  const match = /^\/([^\s/\\`"]+)(?:\s+([\s\S]*))?$/.exec(message.trim());
  return match ? { name: match[1], arguments: (match[2] || "").trim() } : null;
}

export function findSlashCommand(commands: AgentCommand[], name: string) {
  return (
    commands.find((command) => command.name === name) ||
    commands.find((command) => command.aliases?.includes(name))
  );
}

export function commandSuggestions(message: string, commands: AgentCommand[]) {
  const match = /^\/([^\s/\\]*)$/.exec(message);
  if (!match) return null;
  const query = match[1].toLowerCase();
  const rank = (command: AgentCommand) =>
    command.name.startsWith(query)
      ? 0
      : command.aliases?.some((alias) => alias.startsWith(query))
        ? 1
        : 2;
  return commands
    .filter((command) =>
      [
        command.name,
        command.displayName || "",
        ...(command.aliases || []),
        command.description,
      ].some((value) => value.toLowerCase().includes(query)),
    )
    .sort(
      (a, b) =>
        Number(a.kind === "skill") - Number(b.kind === "skill") ||
        rank(a) - rank(b) ||
        (a.displayName || a.name).localeCompare(b.displayName || b.name) ||
        a.name.localeCompare(b.name),
    );
}
