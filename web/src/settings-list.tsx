import { useEffect, type ReactNode } from "react";
import { ArrowLeft, ChevronRight } from "lucide-react";

export type SettingsListItem = {
  id: string;
  title: string;
  description: string;
  status: string;
  icon: ReactNode;
};

export function SettingsList({
  label,
  summary,
  empty,
  items,
  action,
  onSelect,
}: {
  label: string;
  summary: string;
  empty: string;
  items: SettingsListItem[];
  action?: ReactNode;
  onSelect: (id: string) => void;
}) {
  useEffect(() => {
    document.getElementById("settings-content")?.scrollTo(0, 0);
  }, []);
  return (
    <section className="settings-list" aria-label={label}>
      <div className="settings-list-toolbar">
        <span>{summary}</span>
        {action}
      </div>
      {items.length ? (
        <ul className="settings-items">
          {items.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                className="settings-list-item"
                data-entity-id={item.id}
                onClick={() => onSelect(item.id)}
              >
                <span className="settings-list-icon" aria-hidden="true">
                  {item.icon}
                </span>
                <span className="settings-list-copy">
                  <strong>{item.title}</strong>
                  <span title={item.description}>{item.description}</span>
                </span>
                <span className="settings-list-status">{item.status}</span>
                <ChevronRight
                  size={16}
                  className="settings-list-chevron"
                  aria-hidden="true"
                />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <div className="settings-card settings-empty">
          <p>{empty}</p>
        </div>
      )}
    </section>
  );
}

export function SettingsBack({
  label,
  onClick,
}: {
  label: string;
  onClick: () => void;
}) {
  useEffect(() => {
    document.getElementById("settings-content")?.scrollTo(0, 0);
  }, []);
  return (
    <div className="settings-editor-toolbar">
      <button type="button" className="settings-back" onClick={onClick}>
        <ArrowLeft size={15} />
        {label}
      </button>
    </div>
  );
}
