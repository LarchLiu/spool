import Codex from "@lobehub/icons/es/Codex/components/Mono.js";
import Claude from "@lobehub/icons/es/Claude/components/Mono.js";
import CodexColor from "@lobehub/icons/es/Codex/components/Inner.js";
import ClaudeColor from "@lobehub/icons/es/Claude/components/Color.js";
import type { Backend } from "./types";

export function AgentIcon({
  backend,
  size = 14,
  decorative = false,
  colored = false,
}: {
  backend: Backend;
  size?: number;
  decorative?: boolean;
  colored?: boolean;
}) {
  const Icon =
    backend === "claude"
      ? colored
        ? ClaudeColor
        : Claude
      : colored
        ? CodexColor
        : Codex;
  const name = backend === "claude" ? "Claude" : "Codex";
  return (
    <Icon
      className="agent-icon"
      size={size}
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : name}
      aria-hidden={decorative || undefined}
      focusable="false"
    />
  );
}
