import { Fragment, useEffect, useRef } from "react";
import {
  Bug,
  Goal,
  Lightbulb,
  Package,
  Repeat2,
  ScanSearch,
  Terminal,
} from "lucide-react";
import type { Translator } from "./i18n";
import type { AgentCommand } from "./types";

export function SlashCommandMenu({
  t,
  onSelect,
  commands,
  activeIndex,
  onActiveIndex,
}: {
  t: Translator;
  commands: AgentCommand[];
  activeIndex: number;
  onActiveIndex: (index: number) => void;
  onSelect: (command: AgentCommand) => void;
}) {
  const active = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    active.current?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, commands]);
  return (
    <div
      className="slash-command-menu"
      id="slash-command-menu"
      role="listbox"
      aria-label={t("Chat commands")}
    >
      {!commands.length && (
        <p className="slash-command-empty" role="status">
          {t("No matching agent commands")}
        </p>
      )}
      {commands.map((command, index) => (
        <Fragment key={command.name}>
          {command.kind === "skill" &&
            commands[index - 1]?.kind !== "skill" && (
              <div className="slash-command-group" role="presentation">
                {t("Skills")}
              </div>
            )}
          <button
            ref={index === activeIndex ? active : undefined}
            id={`slash-command-${index}`}
            data-command={command.name}
            title={`/${command.name}${command.argumentHint ? " " + command.argumentHint : ""}`}
            type="button"
            role="option"
            aria-selected={index === activeIndex}
            onMouseDown={(event) => event.preventDefault()}
            onMouseEnter={() => onActiveIndex(index)}
            onClick={() => onSelect(command)}
          >
            {command.kind === "skill" ? (
              <Package size={18} aria-hidden="true" />
            ) : command.name === "goal" ? (
              <Goal size={18} aria-hidden="true" />
            ) : command.name === "plan" ? (
              <Lightbulb size={18} aria-hidden="true" />
            ) : command.name === "review" || command.name === "code-review" ? (
              <Bug size={18} aria-hidden="true" />
            ) : command.name === "loop" ? (
              <Repeat2 size={18} aria-hidden="true" />
            ) : command.name === "deep-research" ? (
              <ScanSearch size={18} aria-hidden="true" />
            ) : (
              <Terminal size={18} aria-hidden="true" />
            )}
            <span className="slash-command-content">
              <strong>
                {command.kind === "skill"
                  ? command.displayName || command.name
                  : command.name === "goal"
                    ? t("Goal")
                    : command.name === "plan"
                      ? t("Plan mode")
                      : command.name === "review" ||
                          command.name === "code-review"
                        ? t("Code review")
                        : command.name === "loop"
                          ? t("Loop")
                          : command.name === "deep-research"
                            ? t("Deep research")
                            : `/${command.name}`}
              </strong>
              <small title={command.description}>
                {command.name === "goal" && command.kind !== "skill"
                  ? t("Set a goal to keep pursuing")
                  : command.kind !== "skill" &&
                      (command.name === "review" ||
                        command.name === "code-review")
                    ? t(
                        "Review uncommitted changes or compare against a branch",
                      )
                    : command.kind !== "skill" && command.name === "plan"
                      ? t("Turn plan mode on")
                      : command.description}
              </small>
            </span>
            {command.kind === "skill" &&
              (command.pluginId || command.scope) && (
                <small className="slash-command-source">
                  {command.scope &&
                    (command.scope === "user"
                      ? t("Personal")
                      : command.scope === "repo"
                        ? t("Project")
                        : command.scope === "admin"
                          ? t("Administrator")
                          : t("System"))}
                  {command.scope && command.pluginId && " · "}
                  {command.pluginId &&
                    t("From {plugin}", { plugin: command.pluginId })}
                </small>
              )}
          </button>
        </Fragment>
      ))}
    </div>
  );
}
