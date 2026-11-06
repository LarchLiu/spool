import type { TranscriptPart, Turn } from "./types";
import type { Translator } from "./i18n";
export { turnExchanges, exchangeFiles } from "./turn-exchanges.js";

export function turnWorkLabel(turn: Turn, t: Translator, now: number, answered = false) {
  const running = turn.status === "running";
  if (running && turn.compacting) return t("Compacting context…");
  if (!running && turn.status === "stopped") return t("Stopped");
  if (!running && (!turn.started_at || !turn.finished_at)) return t("Completed");
  const seconds = Math.max(0, Math.round(((running ? now : turn.finished_at!) - turn.started_at) / 1000));
  const working = running && !answered;
  return seconds < 60
    ? t(working ? "Working for {seconds}s" : "Worked for {seconds}s", { seconds })
    : t(working ? "Working for {minutes}m {seconds}s" : "Worked for {minutes}m {seconds}s", {
        minutes: Math.floor(seconds / 60), seconds: seconds % 60,
      });
}

export function turnPresentation(parts: TranscriptPart[], running: boolean) {
  const isText = (part: TranscriptPart) =>
    part.role === "assistant" && part.kind === "text" && !!part.text.trim();
  const isActivity = (part: TranscriptPart) =>
    part.role !== "user" &&
    ["tool_use", "tool_result", "thinking"].includes(part.kind);
  let final = parts.filter((part) => isText(part) && part.phase === "final_answer");
  if (!final.length && !running) {
    // Older Claude/Codex sessions may lack phases: only text after the last
    // tool/thought can be the completed answer, never earlier progress messages.
    let lastActivity = -1;
    parts.forEach((part, index) => {
      if (isActivity(part)) lastActivity = index;
    });
    final = parts.filter(
      (part, index) => index > lastActivity && isText(part) && !part.phase,
    );
  }
  const answers = new Set(final);
  const process = parts.filter(
    (part) => isActivity(part) || (isText(part) && !answers.has(part)),
  );
  let answer = "";
  let previous: TranscriptPart | undefined;
  for (const part of final) {
    const sameMessage = part.id && part.id === previous?.id;
    answer += (answer && !sameMessage ? "\n\n" : "") + part.text;
    previous = part;
  }
  return { process, answer, expanded: running && !final.length };
}
