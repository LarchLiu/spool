import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { Check, FolderOpen } from "lucide-react";
import en from "../../locales/en.json";
import type { MessageKey, Translator } from "./i18n";
import { settingsCall, type SettingsSnapshot } from "./settings-client";
import type { SettingsOperation } from "./settings-autosave";

export type Run = (
  command: SettingsOperation,
) => Promise<SettingsSnapshot | undefined>;
export type PageProps = { data: SettingsSnapshot; run: Run; t: Translator };

export function knownMessage(value: string, t: Translator) {
  return Object.hasOwn(en, value) ? t(value as MessageKey) : value;
}
export function Card({
  title,
  children,
}: {
  title?: string;
  children: ReactNode;
}) {
  return (
    <section className="settings-group">
      {title && <h2>{title}</h2>}
      <div className="settings-card">{children}</div>
    </section>
  );
}
export function Row({
  icon,
  title,
  help,
  children,
  wide = false,
}: {
  icon?: ReactNode;
  title: string;
  help?: ReactNode;
  children?: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className={`settings-row${wide ? " settings-row-wide" : ""}`}>
      <div className="settings-row-heading">
        {icon && (
          <span className="settings-row-icon" aria-hidden="true">
            {icon}
          </span>
        )}
        <div className="settings-row-label">
          <span>{title}</span>
          {help && <p>{help}</p>}
        </div>
      </div>
      {children && <div className="settings-row-control">{children}</div>}
    </div>
  );
}
export function Hint({ children }: { children: ReactNode }) {
  return <p className="settings-hint">{children}</p>;
}

// Copy/reveal actions are not settings mutations. Feedback belongs to this
// button, not to the page-wide queue, disabled state, or snapshot refresh.
export function CopyAddressButton({ t }: { t: Translator }) {
  const [state, setState] = useState<"idle" | "pending" | "copied" | "failed">(
    "idle",
  );
  const pending = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (state !== "copied" && state !== "failed") return;
    const timer = setTimeout(() => setState("idle"), 2000);
    return () => clearTimeout(timer);
  }, [state]);
  const copy = async () => {
    if (pending.current) return;
    pending.current = true;
    setState("pending");
    try {
      await settingsCall<boolean>("Action", { kind: "copy-tsnet-url" });
      if (mounted.current) setState("copied");
    } catch {
      if (mounted.current) setState("failed");
    } finally {
      pending.current = false;
    }
  };
  const message =
    state === "copied"
      ? t("Copied")
      : state === "failed"
        ? t("Could not copy address. Try again.")
        : "";
  return (
    <button
      type="button"
      className="settings-copy-address"
      onClick={() => void copy()}
      aria-label={t("Copy address")}
      aria-busy={state === "pending"}
      title={message || t("Copy address")}
    >
      <span className="settings-copy-labels" aria-hidden="true">
        <span className={state === "copied" ? "" : "active"}>
          {t("Copy address")}
        </span>
        <span className={state === "copied" ? "active" : ""}>
          {t("Copied")}
        </span>
      </span>
      <span className="settings-sr-only" role="status">
        {message}
      </span>
    </button>
  );
}
export function Toggle({
  label,
  value,
  onChange,
  disabled,
}: {
  label: string;
  value: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-label={label}
      aria-checked={value}
      disabled={disabled}
      className="settings-toggle"
      onClick={() => onChange(!value)}
    >
      <span />
    </button>
  );
}
export function TextField({
  title,
  value,
  onChange,
  help,
  type = "text",
  min,
  max,
  required,
  onCommit,
  disabled,
}: {
  title: string;
  value: string | number;
  onChange: (value: string) => void;
  help?: ReactNode;
  type?: string;
  min?: number;
  max?: number;
  required?: boolean;
  onCommit?: () => void;
  disabled?: boolean;
}) {
  return (
    <Row title={title} help={help}>
      <input
        aria-label={title}
        type={type}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        onBlur={
          onCommit
            ? (e) => {
                if (e.currentTarget.reportValidity()) onCommit();
              }
            : undefined
        }
        onKeyDown={
          onCommit
            ? (e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  if (e.currentTarget.reportValidity()) onCommit();
                }
              }
            : undefined
        }
        min={min}
        max={max}
        step={type === "number" ? 1 : undefined}
        required={required}
        autoComplete="off"
        spellCheck={false}
      />
    </Row>
  );
}
export function Save({ title }: { title: string }) {
  return (
    <div className="settings-actions">
      <button className="settings-primary" type="submit">
        <Check size={15} />
        {title}
      </button>
    </div>
  );
}
export function submit(event: FormEvent, action: () => void) {
  event.preventDefault();
  action();
}

export function PathField({
  title,
  value,
  kind,
  onChange,
  t,
}: {
  title: string;
  value: string;
  kind: "directory" | "executable";
  onChange: (value: string) => void;
  t: Translator;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const choose = async () => {
    setPending(true);
    setError("");
    try {
      const selected = await settingsCall<string>("ChoosePath", kind, value);
      if (selected) onChange(selected);
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setPending(false);
    }
  };
  return (
    <Row
      title={title}
      help={error && <span role="alert">{knownMessage(error, t)}</span>}
      wide
    >
      <div className="settings-path">
        <input
          aria-label={title}
          value={value}
          disabled={pending}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
        <button
          type="button"
          aria-label={t(
            kind === "directory" ? "Choose directory…" : "Choose executable…",
          )}
          disabled={pending}
          onClick={() => void choose()}
        >
          <FolderOpen size={16} />
          {t("Browse…")}
        </button>
      </div>
    </Row>
  );
}
