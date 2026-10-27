import { Check } from "lucide-react";
import { effortLabel, type Translator } from "./i18n";
import type { Agent, Backend } from "./types";
import {
  chooseModel,
  effortChoices,
  selectableAgents,
  type ModelSelection,
} from "./model-selection";
import { AgentIcon } from "./agent-icon";

export function AgentModelMenu({
  agents,
  selection,
  lockedBackend,
  t,
  onChange,
}: {
  agents: Agent[];
  selection: ModelSelection;
  lockedBackend?: Backend;
  t: Translator;
  onChange: (selection: ModelSelection) => void;
}) {
  const agent = agents.find((item) => item.backend === selection.backend);
  const model = agent?.catalog.models?.find(
    (item) => item.model === selection.model,
  );
  const levels = effortChoices(selection.backend, model);
  const levelIndex = Math.max(
    0,
    levels.findIndex((level) => level.reasoningEffort === selection.effort),
  );
  return (
    <div
      className="model-popover"
      role="region"
      aria-label={t("Choose agent and model")}
    >
      <div className="agent-model-groups">
        {selectableAgents(agents, lockedBackend).map((item) => {
          const available = item.enabled && item.installed;
          const models = [...(item.catalog.models || [])];
          if (
            item.backend === selection.backend &&
            selection.model &&
            !models.some((model) => model.model === selection.model)
          ) {
            models.unshift({
              model: selection.model,
              displayName: selection.model,
              defaultReasoningEffort: "",
              supportedReasoningEfforts: [],
            });
          }
          return (
            <section
              key={item.backend}
              role="group"
              aria-label={item.backend === "codex" ? "Codex" : "Claude"}
            >
              <h3>
                <span className="agent-group-name">
                  <AgentIcon backend={item.backend} decorative />
                  {item.backend === "codex" ? "Codex" : "Claude"}
                </span>
                <small>
                  {!item.enabled
                    ? t("Disabled")
                    : !item.installed
                      ? t("Not installed")
                      : ""}
                </small>
              </h3>
              {(models.length
                ? models
                : [{ model: "", displayName: t("CLI default") }]
              ).map((model) => {
                const active =
                  item.backend === selection.backend &&
                  model.model === selection.model;
                return (
                  <button
                    type="button"
                    key={model.model}
                    className={
                      "agent-model-option " + (active ? "selected" : "")
                    }
                    aria-pressed={active}
                    disabled={!available}
                    onClick={() => onChange(chooseModel(item, model.model))}
                  >
                    <span>{model.displayName}</span>
                    {active && <Check size={14} />}
                  </button>
                );
              })}
            </section>
          );
        })}
      </div>
      {levels.length ? (
        <div className="model-effort">
          <strong className="effort-heading">
            {effortLabel(selection.effort, t)}
          </strong>
          <div className="effort-track">
            <input
              aria-label={t("Reasoning level")}
              aria-valuetext={effortLabel(selection.effort, t)}
              type="range"
              min={0}
              max={Math.max(0, levels.length - 1)}
              value={levelIndex}
              disabled={levels.length < 2}
              onChange={(event) =>
                onChange({
                  ...selection,
                  effort: levels[Number(event.target.value)].reasoningEffort,
                })
              }
              style={{
                background: `linear-gradient(to right,#369bea ${levels.length < 2 ? 100 : (levelIndex / (levels.length - 1)) * 100}%,var(--track) 0)`,
              }}
            />
            <div className="effort-ticks" aria-hidden="true">
              {(levels.length ? levels : [{}]).map((_, index) => (
                <i
                  key={index}
                  className={index === levelIndex ? "selected" : undefined}
                />
              ))}
            </div>
          </div>
          <div className="effort-caption">
            <span>
              {t(selection.backend === "claude" ? "Default" : "Less")}
            </span>
            <span>{t("More")}</span>
          </div>
        </div>
      ) : (
        <p>{t("This model has no adjustable reasoning level.")}</p>
      )}
      <p>{t("Applies to the next message. Context is preserved.")}</p>
      <p>
        {lockedBackend
          ? t(
              "This chat uses a fixed agent. Create a new chat to use another agent.",
            )
          : t("The first message fixes the agent for this chat.")}
      </p>
    </div>
  );
}
