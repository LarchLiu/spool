import { useState, type FormEvent } from "react";
import { ShieldQuestion } from "lucide-react";
import { api } from "./api";
import type { ApprovalRequest, ApprovalField } from "./types";
import type { MessageKey, Translator } from "./i18n";
import { CustomSelect } from "./custom-select";

export function approvalContent(fields: ApprovalField[], data: FormData) {
  return Object.fromEntries(
    fields.flatMap((field): [string, unknown][] => {
      const value = data.get(field.name);
      if (field.type === "boolean") return [[field.name, value === "on"]];
      if (field.type === "array")
        return [[field.name, data.getAll(field.name)]];
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

function ApprovalInput({ field }: { field: ApprovalField }) {
  const [selected, setSelected] = useState(String(field.default ?? ""));
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
  if (field.options?.length) {
    if (field.type === "array") {
      const defaults = Array.isArray(field.default) ? field.default : [];
      return (
        <div
          className="approval-multiple"
          role="group"
          aria-label={field.label}
        >
          {field.options.map((option) => (
            <label key={option.value}>
              <input
                type="checkbox"
                name={field.name}
                value={option.value}
                defaultChecked={defaults.includes(option.value)}
              />
              {option.label}
            </label>
          ))}
        </div>
      );
    }
    return (
      <div className="approval-choice">
        <input type="hidden" name={field.name} value={selected} />
        <CustomSelect
          label={field.label}
          value={selected}
          options={[{ value: "", label: "—" }, ...field.options]}
          onChange={setSelected}
        />
      </div>
    );
  }
  const numeric = field.type === "integer" || field.type === "number";
  const type = numeric
    ? "number"
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
      step={numeric ? (field.type === "integer" ? 1 : "any") : undefined}
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
  const fields = request.fields ?? [];
  async function decide(allow: boolean, content?: Record<string, unknown>) {
    if (sending || resolved) return;
    setSending(true);
    try {
      await api(`/chats/${chatID}/approvals`, {
        id: request.id,
        turn_id: turnID,
        allow,
        content,
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
    void decide(
      true,
      approvalContent(fields, new FormData(event.currentTarget)),
    );
  }
  return (
    <form
      className="approval-card"
      aria-label={t("Approval required")}
      onSubmit={submit}
    >
      <div className="approval-heading">
        <ShieldQuestion size={18} />
        <strong>{t("Approval required")}</strong>
      </div>
      <p>{request.title}</p>
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
        <p role="status">{t(request.unavailable as MessageKey)}</p>
      )}
      <fieldset className="approval-fields" disabled={sending || resolved}>
        {fields.map((field) => (
          <div key={field.name} className="approval-field">
            <span>
              {field.label}
              {field.required && field.type !== "boolean" ? " *" : ""}
            </span>
            {field.description && <small>{field.description}</small>}
            <ApprovalInput field={field} />
          </div>
        ))}
      </fieldset>
      <small>
        {t(
          "Approval applies only to this request. No permanent rules are saved.",
        )}
      </small>
      <div>
        <button
          type="button"
          className="quiet"
          disabled={sending || resolved}
          onClick={() => void decide(false)}
        >
          {t("Deny")}
        </button>
        <button
          type="submit"
          className="primary"
          disabled={sending || resolved || !!request.unavailable}
        >
          {resolved
            ? t("Decision sent")
            : request.url
              ? t("Confirm authorization")
              : t("Allow once")}
        </button>
      </div>
    </form>
  );
}
