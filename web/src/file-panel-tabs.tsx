import { useId, type ReactNode } from "react";
import { FilePanelButton } from "./file-panel-button";
import "./git-panel.css";

export function FilePanelTabs<T extends string>({
  label,
  value,
  options,
  onChange,
  variant = "segmented",
}: {
  label: string;
  value: T;
  options: { value: T; label: string; icon?: ReactNode }[];
  onChange: (value: T) => void;
  variant?: "underline" | "segmented";
}) {
  const id = useId();
  return (
    <div
      className={`file-panel-tabs ${variant}`}
      role="tablist"
      aria-label={label}
    >
      {options.map((option, index) => (
        <FilePanelButton
          key={option.value}
          id={`${id}-${option.value}`}
          role="tab"
          aria-selected={value === option.value}
          tabIndex={value === option.value ? 0 : -1}
          onClick={() => onChange(option.value)}
          onKeyDown={(event) => {
            const next =
              event.key === "ArrowRight"
                ? (index + 1) % options.length
                : event.key === "ArrowLeft"
                  ? (index + options.length - 1) % options.length
                  : event.key === "Home"
                    ? 0
                    : event.key === "End"
                      ? options.length - 1
                      : -1;
            if (next < 0) return;
            event.preventDefault();
            onChange(options[next].value);
            (
              event.currentTarget.parentElement?.children[next] as HTMLElement
            )?.focus();
          }}
        >
          {option.icon}
          {option.label}
        </FilePanelButton>
      ))}
    </div>
  );
}
