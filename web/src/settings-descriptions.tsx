import { useEffect, useId, useRef, useState } from "react";
import { RefreshCw, Sparkles } from "lucide-react";
import { CustomSelect } from "./custom-select";
import {
  chooseModel,
  effortChoices,
  type ModelSelection,
} from "./model-selection";
import { effortLabel } from "./i18n";
import { usePendingSettingsSave } from "./settings-pending";
import {
  settingsCall,
  type DescriptionEdit,
  type DescriptionResult,
  type DescriptionTarget,
} from "./settings-client";
import type { EntitySummary } from "./types";
import { Card, Row, SettingsError, type PageProps } from "./settings-ui";

export type DescriptionField = {
  id: string;
  label: string;
  summary?: EntitySummary;
  providerDescription?: string;
};

export function descriptionDraft(
  fields: DescriptionField[],
): DescriptionEdit[] {
  return fields.map(({ id, summary }) => ({
    id,
    text: summary?.text ?? "",
    expected_version: summary?.version ?? "",
  }));
}

export function DescriptionEditor({
  data,
  run,
  t,
  target,
  fields,
  disabled = false,
}: PageProps & {
  target: DescriptionTarget;
  fields: DescriptionField[];
  disabled?: boolean;
}) {
  const [items, setItems] = useState(() => descriptionDraft(fields));
  const [original, setOriginal] = useState(() => descriptionDraft(fields));
  const [open, setOpen] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const requestID = useRef<string | undefined>(undefined);
  const live = descriptionDraft(fields);
  const dirty = JSON.stringify(items) !== JSON.stringify(original);
  const liveJSON = JSON.stringify(live);
  const changedElsewhere =
    items.length !== live.length ||
    items.some(
      (item) =>
        live.find((value) => value.id === item.id)?.expected_version !==
        item.expected_version,
    );
  const reset = (next = live) => {
    setItems(next);
    setOriginal(next);
    setSaved(false);
    setError("");
  };
  useEffect(() => {
    if (!dirty) {
      const next = JSON.parse(liveJSON) as DescriptionEdit[];
      setItems(next);
      setOriginal(next);
    }
  }, [liveJSON, dirty]);
  useEffect(
    () => () => {
      if (requestID.current)
        void settingsCall("CancelDescriptionSummary", requestID.current).catch(
          () => {},
        );
    },
    [],
  );
  const save = async () => {
    if (
      disabled ||
      !dirty ||
      generating ||
      changedElsewhere ||
      savingRef.current
    )
      return;
    savingRef.current = true;
    setSaving(true);
    let next;
    try {
      next = await run({
        kind: "save-descriptions",
        descriptions: {
          ...target,
          items: items.filter(
            (item, index) =>
              JSON.stringify(item) !== JSON.stringify(original[index]),
          ),
        },
      });
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
    if (!next) return;
    const updatedFields = fields.map((field) => {
      const agent = next.agents.find((item) => item.backend === target.target);
      const summary =
        target.kind === "project"
          ? next.projects.find((item) => item.id === target.target)?.summary
          : field.id === "agent"
            ? agent?.summary
            : agent?.catalog.models.find(
                (item) => "model:" + item.model === field.id,
              )?.summary;
      return { ...field, summary };
    });
    reset(descriptionDraft(updatedFields));
    setSaved(true);
  };
  usePendingSettingsSave({
    id: `descriptions-${target.kind}-${target.target}`,
    section: target.kind === "project" ? "Projects" : "Agents",
    title: t("Descriptions"),
    label: t("Save descriptions"),
    dirty,
    save,
    cancel: () => reset(),
  });
  return (
    <Card
      title={
        target.kind === "agent"
          ? t("Agent and model descriptions")
          : t("Project description")
      }
    >
      <Row
        title={t("Descriptions")}
        help={t(
          "Edit descriptions yourself or generate them with an agent, then save your changes.",
        )}
      >
        <button
          type="button"
          disabled={disabled || generating || saving}
          onClick={() => setOpen(true)}
        >
          <Sparkles size={14} />
          {t("Summarize descriptions")}
        </button>
      </Row>
      <div className="settings-descriptions">
        {fields.map((field) => {
          const item = items.find((value) => value.id === field.id);
          return (
            <label key={field.id}>
              <span>{field.label}</span>
              {field.providerDescription && (
                <small>{field.providerDescription}</small>
              )}
              <textarea
                aria-label={field.label}
                rows={3}
                maxLength={2000}
                disabled={disabled || generating || saving}
                value={item?.text ?? ""}
                onChange={(event) => {
                  setSaved(false);
                  setItems((previous) =>
                    previous.map((value) =>
                      value.id === field.id
                        ? {
                            id: value.id,
                            expected_version: value.expected_version,
                            text: event.target.value,
                          }
                        : value,
                    ),
                  );
                }}
              />
              {field.summary?.updated_at && (
                <small>
                  {t("Updated {time}", {
                    time: new Date(field.summary.updated_at).toLocaleString(),
                  })}
                </small>
              )}
            </label>
          );
        })}
        {changedElsewhere && dirty && (
          <p role="status">
            {t("Descriptions changed elsewhere. Reload them before saving.")}
          </p>
        )}
        {disabled && (
          <p>{t("Save the project before generating its description.")}</p>
        )}
        <div className="settings-description-actions">
          <button
            type="button"
            disabled={
              disabled || !dirty || generating || saving || changedElsewhere
            }
            onClick={() => void save()}
          >
            {t("Save descriptions")}
          </button>
          <button
            type="button"
            disabled={!dirty || generating || saving}
            onClick={() => reset()}
          >
            {changedElsewhere ? t("Reload descriptions") : t("Discard changes")}
          </button>
          {saved && <span role="status">{t("Saved")}</span>}
        </div>
      </div>
      <SettingsError message={error} t={t} />
      {open && (
        <DescriptionGenerator
          data={data}
          run={run}
          t={t}
          target={target}
          error={error}
          onClose={() => {
            if (requestID.current) {
              void settingsCall(
                "CancelDescriptionSummary",
                requestID.current,
              ).catch(() => {});
              requestID.current = undefined;
            }
            setGenerating(false);
            setOpen(false);
          }}
          onGenerate={async (selection, prompt) => {
            const id = crypto.randomUUID();
            requestID.current = id;
            setGenerating(true);
            setError("");
            try {
              const result = await settingsCall<DescriptionResult>(
                "SummarizeDescriptions",
                {
                  ...target,
                  id,
                  backend: selection.backend,
                  model: selection.model,
                  reasoning_effort: selection.effort,
                  prompt,
                  language: document.documentElement.lang,
                },
              );
              if (requestID.current !== id) return;
              setItems(result.items);
              setSaved(false);
              setOpen(false);
            } catch (error) {
              if (requestID.current === id)
                setError(
                  error instanceof Error ? error.message : String(error),
                );
            } finally {
              if (requestID.current === id) {
                requestID.current = undefined;
                setGenerating(false);
              }
            }
          }}
          generating={generating}
        />
      )}
    </Card>
  );
}

function DescriptionGenerator({
  data,
  run,
  t,
  target,
  generating,
  error,
  onClose,
  onGenerate,
}: PageProps & {
  target: DescriptionTarget;
  generating: boolean;
  error: string;
  onClose: () => void;
  onGenerate: (selection: ModelSelection, prompt: string) => Promise<void>;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const agents = data.agents.filter(
    (agent) => agent.enabled && agent.installed,
  );
  const initial =
    agents.find((agent) => agent.backend === target.target) ?? agents[0];
  const [selection, setSelection] = useState<ModelSelection>(() =>
    initial
      ? chooseModel(
          initial,
          initial.catalog.defaultModel ||
            initial.catalog.models[0]?.model ||
            "",
        )
      : { backend: "codex", model: "", effort: "" },
  );
  const configured = data.description_prompts?.[target.kind];
  const [prompt, setPrompt] = useState(configured?.value ?? "");
  const [promptSaved, setPromptSaved] = useState(false);
  const agent = agents.find((item) => item.backend === selection.backend);
  const model = agent?.catalog.models.find(
    (item) => item.model === selection.model,
  );
  const levels = effortChoices(selection.backend, model);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      className="settings-description-dialog"
      ref={dialog}
      aria-labelledby={titleID}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <h2 id={titleID}>
        {target.kind === "agent"
          ? t("Summarize agent and models")
          : t("Summarize project")}
      </h2>
      <Row title={t("Agent")}>
        <CustomSelect
          label={t("Agent")}
          disabled={generating}
          value={selection.backend}
          options={agents.map((item) => ({
            value: item.backend,
            label: item.backend === "codex" ? "Codex" : "Claude Code",
          }))}
          onChange={(value) => {
            const selected = agents.find((item) => item.backend === value);
            if (selected)
              setSelection(
                chooseModel(
                  selected,
                  selected.catalog.defaultModel ||
                    selected.catalog.models[0]?.model ||
                    "",
                ),
              );
          }}
        />
      </Row>
      <Row title={t("Model")}>
        <CustomSelect
          label={t("Model")}
          disabled={generating}
          value={selection.model}
          options={[
            { value: "", label: t("CLI default") },
            ...(agent?.catalog.models ?? []).map((item) => ({
              value: item.model,
              label: item.displayName,
            })),
          ]}
          onChange={(value) => {
            if (agent) setSelection(chooseModel(agent, value));
          }}
        />
      </Row>
      <Row title={t("Reasoning level")}>
        <CustomSelect
          label={t("Reasoning level")}
          disabled={generating || levels.length === 0}
          value={selection.effort}
          options={[
            { value: "", label: t("Default") },
            ...levels
              .filter((item) => item.reasoningEffort)
              .map((item) => ({
                value: item.reasoningEffort,
                label: effortLabel(item.reasoningEffort, t),
              })),
          ]}
          onChange={(effort) => setSelection({ ...selection, effort })}
        />
      </Row>
      <label className="settings-description-prompt">
        <span>{t("System prompt")}</span>
        <textarea
          rows={8}
          maxLength={4000}
          value={prompt}
          disabled={generating}
          onChange={(event) => {
            setPrompt(event.target.value);
            setPromptSaved(false);
          }}
        />
      </label>
      <div className="settings-description-actions">
        <button
          type="button"
          disabled={generating || !configured}
          onClick={() => {
            setPrompt(configured?.default ?? "");
            setPromptSaved(false);
          }}
        >
          <RefreshCw size={13} />
          {t("Restore default prompt")}
        </button>
        <button
          type="button"
          disabled={generating || !prompt.trim()}
          onClick={async () => {
            if (
              await run({
                kind: "save-description-prompt",
                target: target.kind,
                text: prompt,
              })
            )
              setPromptSaved(true);
          }}
        >
          {t("Save prompt")}
        </button>
        {promptSaved && <span role="status">{t("Saved")}</span>}
      </div>
      {!agents.length && (
        <p role="status">
          {t("Enable and install an agent before generating descriptions.")}
        </p>
      )}
      <SettingsError message={error} t={t} />
      <div className="settings-description-actions settings-description-dialog-actions">
        <button type="button" onClick={onClose}>
          {t("Cancel")}
        </button>
        <button
          type="button"
          className="settings-primary"
          disabled={generating || !agent || !prompt.trim()}
          onClick={() => void onGenerate(selection, prompt)}
        >
          {generating ? t("Summarizing…") : t("Generate descriptions")}
        </button>
      </div>
    </dialog>
  );
}
