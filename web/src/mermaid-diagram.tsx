import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import {
  cachedMermaid,
  renderMermaid,
  type MermaidVector,
} from "./mermaid-renderer.js";
import { MermaidImage } from "./mermaid-image.js";

export type MermaidAction =
  | "Source"
  | "Preview"
  | "Enlarge diagram"
  | "Zoom in"
  | "Zoom out"
  | "Fit diagram"
  | "Rendering diagram…"
  | "Close preview";
type Translator = (key: MermaidAction) => string;
const isDark = () =>
  typeof document !== "undefined" &&
  (document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : window.matchMedia("(prefers-color-scheme: dark)").matches);

export function MermaidDiagram({
  source,
  children,
  showSource = false,
  expanded = false,
  t = (key) => key,
}: {
  source: string;
  children: React.ReactNode;
  showSource?: boolean;
  expanded?: boolean;
  t?: Translator;
}) {
  const [dark, setDark] = useState(isDark);
  const [result, setResult] = useState<{
    source: string;
    dark: boolean;
    value: MermaidVector;
  }>();
  const value =
    result?.source === source && result.dark === dark
      ? result.value
      : cachedMermaid(source, dark);
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setDark(isDark());
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    media.addEventListener("change", update);
    update();
    return () => {
      observer.disconnect();
      media.removeEventListener("change", update);
    };
  }, []);
  useEffect(() => {
    const abort = new AbortController();
    const cached = cachedMermaid(source, dark);
    if (cached) {
      setResult({ source, dark, value: cached });
      return;
    }
    const timer = setTimeout(() => {
      void renderMermaid(source, dark, abort.signal)
        .then((value) => {
          if (!abort.signal.aborted) setResult({ source, dark, value });
        })
        .catch(() => {
          if (!abort.signal.aborted)
            setResult({ source, dark, value: { state: "error" } });
        });
    }, 120);
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [source, dark]);
  const visible = value?.state === "ready" && !showSource;
  return (
    <div className="markdown-mermaid" data-ready={visible}>
      {value?.state === "ready" && (
        <MermaidImage
          vector={value}
          expanded={expanded}
          t={t}
          onError={() => setResult({ source, dark, value: { state: "error" } })}
        />
      )}
      {!visible &&
        (showSource || !expanded || value?.state === "error") &&
        children}
      {expanded && !showSource && !value && (
        <div className="mermaid-loading" role="status">
          {t("Rendering diagram…")}
        </div>
      )}
    </div>
  );
}

export function MermaidPreview({
  source,
  children,
  t,
  onClose,
}: {
  source: string;
  children: React.ReactNode;
  t: Translator;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const current = dialog.current;
    current?.showModal();
    return () => current?.close();
  }, []);
  const close = () => {
    dialog.current?.close();
    onClose();
  };
  return createPortal(
    <dialog
      ref={dialog}
      className="mermaid-preview"
      aria-label={t("Enlarge diagram")}
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) close();
      }}
    >
      <div className="image-preview-toolbar">
        <span>Mermaid</span>
        <button
          autoFocus
          type="button"
          className="icon"
          aria-label={t("Close preview")}
          title={t("Close preview")}
          onClick={close}
        >
          <X size={20} />
        </button>
      </div>
      <MermaidDiagram source={source} expanded t={t}>
        {children}
      </MermaidDiagram>
    </dialog>,
    document.body,
  );
}
