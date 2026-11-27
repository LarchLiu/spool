import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, ShieldCheck, ShieldQuestion } from "lucide-react";
import { api, APIError } from "./api";
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
  fullAccess: "Full access",
  acceptEdits: "Accept edits",
  plan: "Plan",
  dontAsk: "Don't ask",
  bypassPermissions: "Bypass permissions",
};
const descriptions: Record<string, MessageKey> = {
  ask: "You approve requests outside the sandbox.",
  manual: "Confirm operations that require permission.",
  auto: "The agent reviews approval requests automatically.",
  fullAccess:
    "Allow file and network access outside the project without approval.",
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
  const [save, setSave] = useState({ key: "", pending: false, error: "" });
  const saving = useRef(false);
  const activeKey = useRef(key);
  activeKey.current = key;
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!project || !enabled) return;
    let cancelled = false;
    let attempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController;
    const params = new URLSearchParams({ project_id: project, backend });
    if (chatID) params.set("chat_id", chatID);
    const load = async () => {
      const request = new AbortController();
      controller = request;
      const timeout = setTimeout(() => request.abort(), 25_000);
      setResult({ key });
      try {
        const catalog = await api<PermissionCatalog | null>(
          `/permissions?${params}`,
          undefined,
          request.signal,
        );
        if (!cancelled)
          setResult({ key, catalog: normalizeCatalog(catalog, backend) });
      } catch (error) {
        if (cancelled) return;
        setResult({ key, failed: true });
        // Retry only this read. Message submission is never replayed.
        if (!(error instanceof APIError) || error.status >= 500) {
          const delay = [1_000, 3_000][attempt++];
          if (delay !== undefined) retryTimer = setTimeout(load, delay);
        }
      } finally {
        clearTimeout(timeout);
      }
    };
    void load();
    return () => {
      cancelled = true;
      controller.abort();
      clearTimeout(retryTimer);
    };
  }, [key, enabled, revision, savedMode]);
  const catalog = result.key === key ? result.catalog : undefined;
  const mode =
    catalog?.current ||
    (choice.key === key ? choice.mode : savedMode) ||
    catalog?.default ||
    (backend === "claude" ? "acceptEdits" : "ask");
  return {
    catalog,
    mode,
    loading: !catalog && !(result.key === key && result.failed),
    failed: result.key === key && !!result.failed,
    saving: save.pending,
    saveError: save.key === key ? save.error : "",
    ready:
      !save.pending &&
      !!catalog?.modes?.some((item) => item.id === mode && item.available),
    choose: async (mode: string) => {
      if (saving.current) return false;
      saving.current = true;
      setSave({ key, pending: true, error: "" });
      const params = new URLSearchParams({ project_id: project, backend });
      if (chatID) params.set("chat_id", chatID);
      try {
        const updated = await api<PermissionCatalog>(`/permissions?${params}`, {
          mode,
        });
        if (activeKey.current === key)
          setResult({ key, catalog: normalizeCatalog(updated, backend) });
        else {
          // Navigation may have loaded the next chat before this save finished.
          // Discover again in its own directory, using the newly saved default.
          setResult({ key: "" });
          setRevision((v) => v + 1);
        }
        setSave({ key, pending: false, error: "" });
        return activeKey.current === key;
      } catch (error) {
        setSave({
          key,
          pending: false,
          error: error instanceof Error ? error.message : String(error),
        });
        return false;
      } finally {
        saving.current = false;
      }
    },
    adoptChat: (id: string) =>
      setChoice({ key: `${project}/${id}/${backend}`, mode }),
    retry: () => setRevision((v) => v + 1),
  };
}

export function PermissionStatus({
  value,
  t,
  onChoose,
}: {
  value: ReturnType<typeof usePermissionModes>;
  t: Translator;
  onChoose: () => void;
}) {
  if (value.saveError)
    return (
      <div className="connection permission-status" role="alert">
        {value.saveError}
      </div>
    );
  if (value.ready || value.loading || value.saving) return null;
  return (
    <div className="connection permission-status" role="alert">
      {t(
        value.failed
          ? "Permission discovery is unavailable"
          : "Permission mode is unavailable for this project",
      )}
      {value.failed && (
        <button type="button" onClick={value.retry}>
          {t("Retry")}
        </button>
      )}
      {!value.failed && (
        <button type="button" onClick={onChoose}>
          {t("Permissions")}
        </button>
      )}
    </div>
  );
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
  const unrestrictedMode = backend === "codex" ? "fullAccess" : "bypassPermissions";
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
        className={`permission-summary${value.mode === unrestrictedMode ? " danger" : ""}`}
        disabled={disabled || value.saving}
        aria-label={`${t("Permissions")}: ${label}`}
        title={`${t("Permissions")}: ${label}`}
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
          {value.saving && <p>{t("Saving…")}</p>}
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
                item.id === unrestrictedMode
                  ? "permission-option danger"
                  : "permission-option"
              }
              disabled={!item.available || value.saving}
              aria-pressed={value.mode === item.id}
              onClick={async () => {
                if (item.id === unrestrictedMode && value.mode !== item.id) {
                  setConfirmBypass(true);
                  return;
                }
                if (await value.choose(item.id)) onOpen(false);
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
              <p>{t(descriptions[unrestrictedMode])}</p>
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
                  disabled={value.saving}
                  onClick={async () => {
                    if (await value.choose(unrestrictedMode)) onOpen(false);
                  }}
                >
                  {t(backend === "codex" ? "Enable full access" : "Enable bypass")}
                </button>
              </div>
            </div>
          )}
          {backend === "codex" && (
            <p className="permission-scope">
              {t(
                value.mode === "fullAccess"
                  ? "Full access · no sandbox or approval prompts"
                  : "Project sandbox · network access requires approval",
              )}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
