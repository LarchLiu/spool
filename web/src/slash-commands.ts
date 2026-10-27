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
