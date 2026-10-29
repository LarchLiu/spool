import type { ManagedAgent } from "./settings-client";

export function automationModelChoices(
  agent: ManagedAgent | undefined,
  model: string,
) {
  const seen = new Set<string>();
  const models = (agent?.catalog.models || []).filter((entry) => {
    if (!entry.model || seen.has(entry.model)) return false;
    seen.add(entry.model);
    return true;
  });
  const selected = models.find(
    (entry) => entry.model === (model || agent?.catalog.defaultModel),
  );
  const efforts = [
    ...new Set(
      (selected?.supportedReasoningEfforts || [])
        .map((entry) => entry.reasoningEffort)
        .filter(Boolean),
    ),
  ];
  return { models, efforts, selected };
}

export function chooseAutomationModel(
  agent: ManagedAgent | undefined,
  model: string,
  effort: string,
) {
  const { efforts } = automationModelChoices(agent, model);
  return { model, effort: efforts.includes(effort) ? effort : "" };
}
