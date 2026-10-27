import Codex from "@lobehub/icons/es/Codex/components/Mono.js";
import Claude from "@lobehub/icons/es/Claude/components/Mono.js";
import type { Backend } from "./types";

export function AgentIcon({
  backend,
  size = 14,
  decorative = false,
}: {
  backend: Backend;
  size?: number;
  decorative?: boolean;
}) {
  const Icon = backend === "claude" ? Claude : Codex;
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
