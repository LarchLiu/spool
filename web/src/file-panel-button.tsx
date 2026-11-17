import type { ButtonHTMLAttributes } from "react";

/** Shared panel action: semantic keyboard behavior with fully custom appearance. */
export function FilePanelButton({
  className = "",
  type = "button",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      type={type}
      className={`file-panel-button ${className}`}
    />
  );
}
