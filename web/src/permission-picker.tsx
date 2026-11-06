import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, ShieldCheck, ShieldQuestion } from "lucide-react";
import { api } from "./api";
import type { Backend, PermissionCatalog } from "./types";
import type { MessageKey, Translator } from "./i18n";

export { ApprovalCard } from "./approval-card";

function normalizeCatalog(
  catalog: PermissionCatalog | null | undefined,
  backend: Backend,
): PermissionCatalog {
  return {
    ...(catalog || {}),
    backend,
    default: catalog?.default || "",
    current: catalog?.current || "",
    modes: Array.isArray(catalog?.modes) ? catalog.modes : [],
  };
}
const labels: Record<string, MessageKey> = {
  ask: "Ask for approval",
  manual: "Manual",
  auto: "Auto",
  acceptEdits: "Accept edits",
  plan: "Plan",
  dontAsk: "Don't ask",
  bypassPermissions: "Bypass permissions",
};
const descriptions: Record<string, MessageKey> = {
  ask: "You approve requests outside the sandbox.",
  manual: "Confirm operations that require permission.",
  auto: "The agent reviews approval requests automatically.",
  acceptEdits: "Allow file edits; ask for other protected operations.",
  plan: "Plan before making changes.",
  dontAsk: "Deny operations that are not already permitted.",
  bypassPermissions: "Skip permission checks. Only use in trusted projects.",
};

export function usePermissionModes(
  project: string,
  chatID: string,
  backend: Backend,
  enabled: boolean,
  savedMode?: string,
) {
  const key = `${project}/${chatID}/${backend}`;
  const [result, setResult] = useState<{
    key: string;
    catalog?: PermissionCatalog;
    failed?: boolean;
  }>({ key: "" });
  const [choice, setChoice] = useState({ key: "", mode: "" });
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!project || !enabled) return;
    let cancelled = false;
    setResult({ key });
    const params = new URLSearchParams({ project_id: project, backend });
    if (chatID) params.set("chat_id", chatID);
    api<PermissionCatalog | null>(`/permissions?${params}`)
      .then((catalog) => {
        if (!cancelled)
          setResult({ key, catalog: normalizeCatalog(catalog, backend) });
      })
      .catch(() => {
        if (!cancelled) setResult({ key, failed: true });
      });
    return () => {
      cancelled = true;
    };
  }, [key, enabled, revision]);
  const catalog = result.key === key ? result.catalog : undefined;
  const mode =
    choice.key === key
      ? choice.mode
      : savedMode ||
        catalog?.current ||
        catalog?.default ||
        (backend === "claude" ? "acceptEdits" : "ask");
  return {
    catalog,
    mode,
    loading: !catalog && !(result.key === key && result.failed),
    failed: result.key === key && !!result.failed,
    ready: !!catalog?.modes?.some(
      (item) => item.id === mode && item.available,
    ),
    choose: (mode: string) => setChoice({ key, mode }),
    adoptChat: (id: string) =>
      setChoice({ key: `${project}/${id}/${backend}`, mode }),
    retry: () => setRevision((v) => v + 1),
  };
}

export function PermissionPicker({
  backend,
  value,
  disabled,
  open,
  onOpen,
  t,
}: {
  backend: Backend;
  value: ReturnType<typeof usePermissionModes>;
  disabled: boolean;
  open: boolean;
  onOpen: (open: boolean) => void;
  t: Translator;
}) {
  const root = useRef<HTMLDivElement>(null);
  const [confirmBypass, setConfirmBypass] = useState(false);
  useEffect(() => {
    if (!open) setConfirmBypass(false);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const click = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) onOpen(false);
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onOpen(false);
        root.current?.querySelector("button")?.focus();
      }
    };
    document.addEventListener("pointerdown", click);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", click);
      document.removeEventListener("keydown", key);
    };
  }, [open]);
  const label =
    value.mode === "auto" && backend === "codex"
      ? t("Approve for me")
      : t(labels[value.mode] || "Permissions");
  const Icon =
    value.mode === "ask" || value.mode === "manual"
      ? ShieldQuestion
      : ShieldCheck;
  return (
    <div className="permission-picker" ref={root}>
      <button
        type="button"
        className={`permission-summary${value.mode === "bypassPermissions" ? " danger" : ""}`}
        disabled={disabled}
        aria-label={`${t("Permissions")}: ${label}`}
        aria-expanded={open}
        onClick={() => onOpen(!open)}
      >
        <Icon size={15} />
        <span>{label}</span>
        <ChevronDown size={11} />
      </button>
      {open && (
        <div
          className="permission-popover"
          role="region"
          aria-label={t("Permissions")}
        >
          <h3>
            {t("Permissions")}
            <small>{backend === "codex" ? "Codex" : "Claude"}</small>
          </h3>
          {value.loading && <p>{t("Checking project permissions…")}</p>}
          {value.failed && (
            <p>
              {t("Permission discovery is unavailable")}{" "}
              <button type="button" onClick={value.retry}>
                {t("Retry")}
              </button>
            </p>
          )}
          {value.catalog?.modes.map((item) => (
            <button
              key={item.id}
              type="button"
              className={
                item.id === "bypassPermissions"
                  ? "permission-option danger"
                  : "permission-option"
              }
              disabled={!item.available}
              aria-pressed={value.mode === item.id}
              onClick={() => {
                if (item.id === "bypassPermissions" && value.mode !== item.id) {
                  setConfirmBypass(true);
                  return;
                }
                value.choose(item.id);
                onOpen(false);
              }}
            >
              <span>
                <strong>
                  {item.id === "auto" && backend === "codex"
                    ? t("Approve for me")
                    : t(labels[item.id] || "Permissions")}
                </strong>
                <small>
                  {item.reason
                    ? t(item.reason as MessageKey)
                    : t(descriptions[item.id] || "Permissions")}
                </small>
              </span>
              {value.mode === item.id && <Check size={15} />}
            </button>
          ))}
          {confirmBypass && (
            <div className="bypass-confirmation" role="alert">
              <p>
                {t("Skip permission checks. Only use in trusted projects.")}
              </p>
              <div>
                <button
                  type="button"
                  className="quiet"
                  onClick={() => setConfirmBypass(false)}
                >
                  {t("Cancel")}
                </button>
                <button
                  type="button"
                  className="quiet danger"
                  onClick={() => {
                    value.choose("bypassPermissions");
                    onOpen(false);
                  }}
                >
                  {t("Enable bypass")}
                </button>
              </div>
            </div>
          )}
          {backend === "codex" && (
            <p className="permission-scope">
              {t("Project sandbox · network access requires approval")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
