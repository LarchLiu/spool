import { useState, useRef, type FormEvent } from "react";
import { ShieldQuestion } from "lucide-react";
import { api } from "./api";
import type { ApprovalRequest, ApprovalField, ApprovalDecision } from "./types";
import type { MessageKey, Translator } from "./i18n";
import { CustomSelect } from "./custom-select";

export function approvalContent(fields: ApprovalField[], data: FormData) {
  return Object.fromEntries(
    fields.flatMap((field): [string, unknown][] => {
      const value = data.get(field.name);
      if (field.type === "boolean") return [[field.name, value === "on"]];
      if (field.type === "array")
        return [[field.name, data.getAll(field.name)]];
      if (field.type === "json")
        return value === null || value === ""
          ? []
          : [[field.name, JSON.parse(String(value))]];
      if (value === null || (value === "" && !field.required)) return [];
      return [
        [
          field.name,
          field.type === "number" || field.type === "integer"
            ? Number(value)
            : value,
        ],
      ];
    }),
  );
}

function ApprovalInput({ field, t }: { field: ApprovalField; t: Translator }) {
  const [selected, setSelected] = useState(String(field.default ?? ""));
  const [other, setOther] = useState("");
  const freeAnswer = field.allow_other && (
    <input
      aria-label={`${field.label}: ${t("Other answer")}`}
      type={field.secret ? "password" : "text"}
      placeholder={t("Other answer")}
      value={other}
      onChange={(event) => {
        setOther(event.target.value);
        if (field.type !== "array") setSelected(event.target.value);
      }}
    />
  );
  if (field.type === "json")
    return (
      <textarea
        name={field.name}
        aria-label={field.label}
        defaultValue={JSON.stringify(field.default ?? {}, null, 2)}
        spellCheck={false}
        rows={6}
      />
    );
  if (field.type === "boolean") {
    return (
      <input
        type="checkbox"
        name={field.name}
        aria-label={field.label}
        defaultChecked={field.default === true}
      />
    );
  }
  if (field.type === "array" || field.options?.length) {
    if (field.type === "array") {
      const defaults = Array.isArray(field.default) ? field.default : [];
      return (
        <div
          className="approval-multiple"
          role="group"
          aria-label={field.label}
        >
          {(field.options ?? []).map((option) => (
            <label key={option.value}>
              <input
                type="checkbox"
                name={field.name}
                value={option.value}
                defaultChecked={defaults.includes(option.value)}
              />
              <span>
                {t(option.label as MessageKey)}
                {option.description && (
                  <small>{t(option.description as MessageKey)}</small>
                )}
              </span>
            </label>
          ))}
          {freeAnswer}
          {other && <input type="hidden" name={field.name} value={other} />}
        </div>
      );
    }
    return (
      <div className="approval-choice">
        <input type="hidden" name={field.name} value={selected} />
        <CustomSelect
          label={field.label}
          value={selected}
          options={[
            { value: "", label: "—" },
            ...(field.options ?? []).map((option) => ({
              ...option,
              label: t(option.label as MessageKey),
            })),
            ...(other &&
            !field.options?.some((option) => option.value === other)
              ? [{ value: other, label: t("Other answer") }]
              : []),
          ]}
          onChange={(value) => {
            setSelected(value);
            setOther("");
          }}
        />
        {freeAnswer}
      </div>
    );
  }
  if (field.multiline)
    return (
      <textarea
        name={field.name}
        aria-label={field.label}
        required={field.required}
        defaultValue={String(field.default ?? "")}
        rows={3}
      />
    );
  const numeric = field.type === "integer" || field.type === "number";
  const type = numeric
    ? "number"
    : field.secret
      ? "password"
      : field.format === "email"
        ? "email"
        : field.format === "uri"
          ? "url"
          : "text";
  return (
    <input
      name={field.name}
      aria-label={field.label}
      type={type}
      required={field.required}
      defaultValue={String(field.default ?? "")}
      min={field.minimum}
      max={field.maximum}
      step={
        numeric
          ? (field.step ?? (field.type === "integer" ? 1 : "any"))
          : undefined
      }
      minLength={field.min_length}
      maxLength={field.max_length}
    />
  );
}

export function ApprovalCard({
  request,
  chatID,
  turnID,
  t,
  onError,
}: {
  request: ApprovalRequest;
  chatID: string;
  turnID: string;
  t: Translator;
  onError: (error: unknown) => void;
}) {
  const [sending, setSending] = useState(false);
  const [resolved, setResolved] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const fields = request.fields ?? [];
  const decisions = request.decisions ?? [
    { id: "deny", label: "Deny", kind: "deny" },
    {
      id: "allow",
      label: request.url ? "Confirm authorization" : "Allow once",
      kind: "approve",
    },
  ];
  const deny =
    decisions.find((choice) => choice.id === "deny") ??
    decisions.find(
      (choice) => choice.kind === "deny" || choice.kind === "cancel",
    );
  const options = decisions.filter((choice) => choice !== deny);
  const [choiceID, setChoiceID] = useState(
    options.find((choice) => choice.id === "allow")?.id ??
      options.find(
        (choice) => choice.kind === "approve" || choice.kind === "submit",
      )?.id ??
      options[0]?.id,
  );
  const selected =
    options.find((choice) => choice.id === choiceID) ?? options[0];
  const needsContent = (choice: ApprovalDecision) =>
    choice.kind === "approve" || choice.kind === "submit";
  const translate = (value: string) => t(value as MessageKey) || value;
  async function decide(choice: ApprovalDecision) {
    if (sending || resolved || !form.current) return;
    const allow = needsContent(choice);
    if (allow && (!form.current.reportValidity() || request.unavailable))
      return;
    try {
      const data = new FormData(form.current);
      const content = allow
        ? request.json_form
          ? JSON.parse(String(data.get("spool-json-form")))
          : approvalContent(fields, data)
        : undefined;
      setSending(true);
      await api(`/chats/${chatID}/approvals`, {
        id: request.id,
        turn_id: turnID,
        allow,
        decision: choice.id,
        content,
        feedback: request.feedback
          ? String(data.get("spool-decision-feedback") ?? "")
          : undefined,
      });
      setResolved(true);
    } catch (error) {
      onError(error);
    } finally {
      setSending(false);
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    // An Enter key in a field must not silently select a persistent policy.
    if (selected && (event.nativeEvent as SubmitEvent).submitter)
      void decide(selected);
  }
  const blocked = sending || resolved;
  const affirmativeBlocked = blocked || !!request.unavailable;
  return (
    <form
      ref={form}
      className="approval-card"
      aria-label={t(
        request.kind === "input" ? "Your input is needed" : "Approval required",
      )}
      onSubmit={submit}
      onKeyDown={(event) => {
        if (
          event.key === "Enter" &&
          !event.nativeEvent.isComposing &&
          event.target instanceof HTMLInputElement
        )
          event.preventDefault();
      }}
    >
      <div className="approval-heading">
        <ShieldQuestion size={18} />
        <strong>
          {t(
            request.kind === "input"
              ? "Your input is needed"
              : "Approval required",
          )}
        </strong>
      </div>
      <p>{translate(request.title)}</p>
      {request.reason && <p>{request.reason}</p>}
      {request.detail && <pre>{request.detail}</pre>}
      {request.url && (
        <p>
          <a href={request.url} target="_blank" rel="noopener noreferrer">
            {t("Open authorization page")}
          </a>
          <br />
          <small>
            {t("Complete authorization on the linked page, then confirm here.")}
          </small>
        </p>
      )}
      {request.unavailable && (
        <p role="status">{translate(request.unavailable)}</p>
      )}
      <fieldset className="approval-fields" disabled={blocked}>
        {fields.map((field) => (
          <div key={field.name} className="approval-field">
            <span>
              {translate(field.label)}
              {field.required && field.type !== "boolean" ? " *" : ""}
            </span>
            {field.description && <small>{translate(field.description)}</small>}
            <ApprovalInput field={field} t={t} />
          </div>
        ))}
        {request.json_form && (
          <div className="approval-field">
            <span>{t("Form values (JSON)")}</span>
            <details>
              <summary>{t("Requested form schema")}</summary>
              <pre>{JSON.stringify(request.schema, null, 2)}</pre>
            </details>
            <textarea
              name="spool-json-form"
              aria-label={t("Form values (JSON)")}
              defaultValue={JSON.stringify(
                schemaDefaults(request.schema ?? {}),
                null,
                2,
              )}
              rows={8}
              spellCheck={false}
            />
          </div>
        )}
        {request.feedback && (
          <label className="approval-field">
            <span>{t("Feedback for denial (optional)")}</span>
            <textarea
              name="spool-decision-feedback"
              rows={2}
              maxLength={4096}
            />
          </label>
        )}
      </fieldset>
      {request.decisions?.length ? null : (
        <small>
          {t(
            "Approval applies only to this request. No permanent rules are saved.",
          )}
        </small>
      )}
      {selected?.description && (
        <pre className="approval-decision-detail">
          {translate(selected.description)}
        </pre>
      )}
      <div className="approval-actions">
        {deny && (
          <button
            type="button"
            className="quiet"
            disabled={blocked}
            onClick={() => void decide(deny)}
          >
            {translate(deny.label)}
          </button>
        )}
        {selected && (
          <div className="approval-split">
            <button
              type={needsContent(selected) ? "submit" : "button"}
              className="primary"
              disabled={needsContent(selected) ? affirmativeBlocked : blocked}
              onClick={
                needsContent(selected) ? undefined : () => void decide(selected)
              }
            >
              {resolved ? t("Decision sent") : translate(selected.label)}
            </button>
            {options.length > 1 && (
              <CustomSelect
                iconOnly
                label={t("Choose an approval option")}
                value={selected.id}
                disabled={blocked}
                options={options.map((choice) => ({
                  value: choice.id,
                  label: translate(choice.label),
                  description:
                    choice.description && translate(choice.description),
                  disabled: !!request.unavailable && needsContent(choice),
                }))}
                onChange={setChoiceID}
              />
            )}
          </div>
        )}
      </div>
    </form>
  );
}

export function schemaDefaults(
  schema: Record<string, unknown>,
  depth = 0,
): unknown {
  if (depth > 12) return null;
  if (schema.default !== undefined) return schema.default;
  if (schema.const !== undefined) return schema.const;
  if (Array.isArray(schema.enum)) return schema.enum[0];
  if (schema.type === "object" || schema.properties)
    return Object.fromEntries(
      Object.entries(
        (schema.properties ?? {}) as Record<string, Record<string, unknown>>,
      ).map(([name, value]) => [name, schemaDefaults(value, depth + 1)]),
    );
  if (schema.type === "array") return [];
  if (schema.type === "boolean") return false;
  if (schema.type === "number" || schema.type === "integer")
    return schema.minimum ?? 0;
  return "";
}
