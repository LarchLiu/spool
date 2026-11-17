import type { ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

export function SidebarSection({
  id,
  title,
  collapsed,
  onToggle,
  children,
}: {
  id: string;
  title: string;
  collapsed: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <section className={"nav-section " + id} aria-labelledby={id + "-label"}>
      <h2 id={id + "-label"}>
        <button
          type="button"
          className="nav-section-toggle"
          aria-expanded={!collapsed}
          aria-controls={id + "-content"}
          onClick={onToggle}
        >
          <span>{title}</span>
          {collapsed ? (
            <ChevronRight size={14} aria-hidden="true" />
          ) : (
            <ChevronDown size={14} aria-hidden="true" />
          )}
        </button>
      </h2>
      <div id={id + "-content"} hidden={collapsed}>
        {children}
      </div>
    </section>
  );
}
