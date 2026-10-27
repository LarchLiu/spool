import type { Agent, Backend, Chat, Model } from "./types";

export type ModelSelection = {
  backend: Backend;
  model: string;
  effort: string;
};

export function selectableAgents(
  agents: Agent[],
  lockedBackend?: Backend,
): Agent[] {
  return lockedBackend
    ? agents.filter((agent) => agent.backend === lockedBackend)
    : agents;
}

export function chooseModel(agent: Agent, model: string): ModelSelection {
  const entry = agent.catalog.models?.find((item) => item.model === model);
  const effort =
    (model === agent.catalog.defaultModel ? agent.catalog.defaultEffort : "") ||
    entry?.defaultReasoningEffort ||
    "";
  return {
    backend: agent.backend,
    model,
    effort:
      agent.backend === "claude" &&
      !entry?.supportedReasoningEfforts?.some(
        (level) => level.reasoningEffort === effort,
      )
        ? ""
        : effort,
  };
}

export function effortChoices(backend: Backend, model?: Model) {
  const levels = model?.supportedReasoningEfforts || [];
  // Claude does not advertise a default level; leave that choice to the CLI.
  return backend === "claude" && levels.length
    ? [{ reasoningEffort: "", description: "" }, ...levels]
    : levels;
}

export function chatSelection(agents: Agent[], chat: Chat): ModelSelection {
  const agent = agents.find((item) => item.backend === chat.backend);
  if (!agent)
    return {
      backend: chat.backend,
      model: chat.model || "",
      effort: chat.reasoning_effort || "",
    };
  const selected = chooseModel(
    agent,
    chat.model ||
      agent.catalog.defaultModel ||
      agent.catalog.models?.[0]?.model ||
      "",
  );
  if (chat.reasoning_effort) selected.effort = chat.reasoning_effort;
  return selected;
}
