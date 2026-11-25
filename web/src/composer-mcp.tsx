import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { ArrowLeft, Check, Plug, X } from "lucide-react";
import type { AgentCommand } from "./types";
import type { MessageKey, Translator } from "./i18n";

export const mcpCommand: AgentCommand = {
  name: "mcp",
  builtin: true,
  kind: "client",
  description: "Choose MCP tools for this chat",
};
export const federationMCP = "spool_federation";
export type ComposerMCPEntry = {
  id: string;
  name: MessageKey;
  description: MessageKey;
  unavailable?: MessageKey;
  requiresTrustedServer?: boolean;
  removeLabel: MessageKey;
  keywords?: string;
};
export const builtinMCPs: readonly ComposerMCPEntry[] = [
  {
    id: federationMCP,
    name: "Remote agents",
    description: "Delegate to agents on trusted servers",
    unavailable: "No trusted servers available",
    requiresTrustedServer: true,
    removeLabel: "Remove remote agent MCP",
    keywords: "跨服 Agent",
  },
];

export function useComposerMCP({
  scope,
  chatID,
  draft,
  focused,
  enabled,
  disabled,
  available,
  onDraft,
  catalog = builtinMCPs,
}: {
  scope: string;
  chatID: string;
  draft: string;
  focused: boolean;
  enabled: boolean;
  disabled: boolean;
  // Whether a trusted server exists. Undefined means not known yet: nothing
  // new can be chosen, and what was chosen is kept.
  available: boolean | undefined;
  onDraft: (value: string) => void;
  catalog?: readonly ComposerMCPEntry[];
}) {
  const [selected, setSelected] = useState<string[]>([]);
  const [active, setActive] = useState({ key: "", index: 0 });
  const [dismissed, setDismissed] = useState(false);
  const previousScope = useRef(scope);
  const adopted = useRef<string | undefined>(undefined);
  useLayoutEffect(() => {
    if (adopted.current !== chatID || previousScope.current !== scope)
      setSelected([]);
    adopted.current = undefined;
    previousScope.current = scope;
    setDismissed(false);
  }, [scope, chatID]);
  const match = /^\/mcp(?:\s+(.*))?$/i.exec(draft);
  const query = (match?.[1] || "").trim().toLowerCase();
  const entries = catalog.map((item) => ({
    ...item,
    available: !item.requiresTrustedServer || available === true,
  }));
  const rows = entries.filter((item) =>
    `${item.id} ${item.name} ${item.description} ${item.keywords ?? ""}`
      .toLowerCase()
      .includes(query),
  );
  const key = `${draft}\n${rows.map((item) => item.id).join(",")}`;
  const index =
    active.key === key
      ? Math.min(active.index, Math.max(0, rows.length - 1))
      : 0;
  const open = enabled && focused && !disabled && !dismissed && !!match;
  useEffect(() => {
    setSelected((prior) => {
      const next = prior.filter((id) =>
        catalog.some(
          (item) =>
            item.id === id &&
            (!item.requiresTrustedServer || available !== false),
        ),
      );
      return next.length === prior.length ? prior : next;
    });
  }, [catalog, available]);
  useEffect(() => {
    setDismissed(false);
  }, [draft]);
  const choose = (id = rows[index]?.id) => {
    if (!rows.some((item) => item.id === id && item.available)) return;
    setSelected((value) =>
      value.includes(id)
        ? value.filter((entry) => entry !== id)
        : [...value, id],
    );
    onDraft("");
  };
  return {
    selected,
    open,
    entries,
    rows,
    index,
    onActive: (index: number) => setActive({ key, index }),
    show: () => {
      onDraft("/mcp ");
      setDismissed(false);
    },
    back: () => onDraft("/"),
    choose,
    remove: (id: string) =>
      setSelected((value) => value.filter((entry) => entry !== id)),
    adoptChat: (id: string) => {
      adopted.current = id;
    },
    onKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => {
      if (!open) return false;
      if (
        ["Enter", "Tab", "ArrowRight"].includes(event.key) &&
        !event.shiftKey &&
        !event.altKey &&
        !event.metaKey
      ) {
        event.preventDefault();
        choose();
        return true;
      }
      if (event.key === "ArrowLeft" || event.key === "Escape") {
        event.preventDefault();
        if (event.key === "ArrowLeft") onDraft("/");
        else setDismissed(true);
        return true;
      }
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        if (rows.length)
          setActive({
            key,
            index:
              (index + (event.key === "ArrowDown" ? 1 : rows.length - 1)) %
              rows.length,
          });
        return true;
      }
      return false;
    },
  };
}

export type ComposerMCP = ReturnType<typeof useComposerMCP>;

export function MCPMenu({ mcp, t }: { mcp: ComposerMCP; t: Translator }) {
  const active = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    active.current?.scrollIntoView({ block: "nearest" });
  }, [mcp.index, mcp.rows.length]);
  return (
    <div
      className="slash-command-menu"
      id="mcp-menu"
      role="listbox"
      aria-label={t("MCP tools")}
    >
      <button
        type="button"
        onMouseDown={(event) => event.preventDefault()}
        onClick={mcp.back}
      >
        <ArrowLeft size={14} />
        <span>mcp</span>
      </button>
      {mcp.rows.length ? (
        mcp.rows.map((item, index) => (
          <button
            key={item.id}
            ref={index === mcp.index ? active : undefined}
            type="button"
            role="option"
            id={`mcp-option-${index}`}
            aria-selected={index === mcp.index}
            aria-disabled={!item.available}
            disabled={!item.available}
            onMouseDown={(event) => event.preventDefault()}
            onMouseEnter={() => mcp.onActive(index)}
            onClick={() => mcp.choose(item.id)}
          >
            <Plug size={18} aria-hidden="true" />
            <span className="slash-command-content">
              <strong>{t(item.name)}</strong>
              <small>
                {t(
                  item.available
                    ? item.description
                    : (item.unavailable ?? item.description),
                )}
              </small>
            </span>
            {mcp.selected.includes(item.id) && (
              <Check size={14} aria-hidden="true" />
            )}
          </button>
        ))
      ) : (
        <p className="slash-command-empty" role="status">
          {t("No matching MCP tools")}
        </p>
      )}
    </div>
  );
}

export function MCPChips({
  mcp,
  t,
  disabled,
}: {
  mcp: ComposerMCP;
  t: Translator;
  disabled?: boolean;
}) {
  if (!mcp.selected.length) return null;
  return (
    <div className="composer-mcp-chips">
      {mcp.entries
        .filter((item) => mcp.selected.includes(item.id))
        .map((item) => (
          <span className="composer-mcp-chip" key={item.id}>
            <Plug size={13} aria-hidden="true" />
            MCP · {t(item.name)}
            <button
              type="button"
              className="icon"
              disabled={disabled}
              aria-label={t(item.removeLabel)}
              onClick={() => mcp.remove(item.id)}
            >
              <X size={13} />
            </button>
          </span>
        ))}
    </div>
  );
}
