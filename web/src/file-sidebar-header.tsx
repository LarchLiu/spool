import { useEffect, useRef, useState } from "react";
import {
  FolderOpen,
  PanelRightClose,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import type { Translator } from "./i18n";

type FileSearch = {
  isOpen: boolean;
  onOpen: () => void;
  onChange: (query: string) => void;
  onClose: () => void;
};

export function FileSidebarHeader({
  name,
  t,
  search,
  onRefresh,
  onClose,
}: {
  name?: string;
  t: Translator;
  search?: FileSearch;
  onRefresh?: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const searchToggle = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);

  useEffect(() => {
    if (search?.isOpen) input.current?.focus();
    else if (restoreFocus.current) {
      searchToggle.current?.focus();
      restoreFocus.current = false;
    }
  }, [search?.isOpen]);

  function closeSearch() {
    restoreFocus.current = true;
    search?.onClose();
    setQuery("");
  }

  return (
    <header className="file-sidebar-header">
      {search?.isOpen ? (
        <div
          className="file-sidebar-search"
          role="search"
          aria-label={t("Search files")}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              closeSearch();
            }
          }}
        >
          <input
            ref={input}
            type="search"
            value={query}
            placeholder={t("Search files")}
            aria-label={t("Search files")}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => {
              setQuery(event.target.value);
              search.onChange(event.target.value);
            }}
          />
        </div>
      ) : (
        <div className="file-sidebar-title">
          <FolderOpen size={16} />
          <strong title={name}>{name || t("Files")}</strong>
        </div>
      )}
      <div className="file-sidebar-actions">
        {search && (
          <button
            ref={searchToggle}
            className="icon"
            onClick={() => {
              if (search.isOpen) closeSearch();
              else {
                setQuery("");
                search.onOpen();
              }
            }}
            aria-expanded={search.isOpen}
            title={t(search.isOpen ? "Close file search" : "Search files")}
            aria-label={t(search.isOpen ? "Close file search" : "Search files")}
          >
            {search.isOpen ? <X size={18} /> : <Search size={18} />}
          </button>
        )}
        {onRefresh && (
          <button
            className="icon"
            onClick={onRefresh}
            title={t("Refresh files")}
            aria-label={t("Refresh files")}
          >
            <RefreshCw size={18} />
          </button>
        )}
        <button
          className="icon"
          onClick={onClose}
          aria-label={t("Collapse file sidebar")}
          title={t("Collapse file sidebar")}
        >
          <PanelRightClose size={18} />
        </button>
      </div>
    </header>
  );
}
