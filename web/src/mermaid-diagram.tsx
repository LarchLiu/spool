/// <reference types="vite/client" />
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Minus, Plus, Scan, X } from "lucide-react";

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

let documentPromise: Promise<string> | undefined;
export function loadMermaidDocument() {
  return (documentPromise ??= Promise.all([
    import("./mermaid-document.js"),
    import("mermaid/dist/mermaid.min.js?url&no-inline"),
    import("./mermaid-frame.js?url&no-inline"),
  ])
    .then(
      ([{ mermaidDocument }, { default: library }, { default: renderer }]) =>
        mermaidDocument(
          [library, renderer].map((url) => ({
            src: new URL(url, document.baseURI).href,
          })),
        ),
    )
    .catch((error) => {
      documentPromise = undefined;
      throw error;
    }));
}

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
  const frame = useRef<HTMLIFrameElement>(null);
  const generation = useRef(0);
  const renderSource = useRef(source);
  const [html, setHtml] = useState<string>();
  const [loadError, setLoadError] = useState(false);
  const [result, setResult] = useState<{
    source: string;
    state: "loading" | "ready" | "error";
  }>({ source, state: "loading" });
  const state = loadError
    ? "error"
    : result.source === source
      ? result.state
      : "loading";
  const [height, setHeight] = useState(240);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let active = true;
    void loadMermaidDocument()
      .then((value) => {
        if (active) setHtml(value);
      })
      .catch(() => {
        if (active) setLoadError(true);
      });
    return () => {
      active = false;
    };
  }, []);
  useEffect(() => {
    const receive = (event: MessageEvent) => {
      const data = event.data;
      if (
        event.source !== frame.current?.contentWindow ||
        data?.type !== "spool:mermaid" ||
        data.generation !== generation.current
      )
        return;
      if (typeof data.ready !== "boolean" || !Number.isFinite(data.height))
        return;
      setResult({
        source: renderSource.current,
        state: data.ready ? "ready" : "error",
      });
      if (data.ready) setHeight(Math.max(1, Math.min(20000, data.height)));
    };
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, []);
  useEffect(() => {
    if (!loaded) return;
    const root = document.documentElement;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    let timer: ReturnType<typeof setTimeout>;
    const render = () => {
      const current = ++generation.current;
      renderSource.current = source;
      setResult({ source, state: "loading" });
      clearTimeout(timer);
      timer = setTimeout(
        () =>
          frame.current?.contentWindow?.postMessage(
            {
              type: "spool:render-mermaid",
              source,
              dark: root.dataset.theme
                ? root.dataset.theme === "dark"
                : media.matches,
              generation: current,
              expanded,
            },
            "*",
          ),
        120,
      );
    };
    render();
    const observer = new MutationObserver(render);
    observer.observe(root, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    media.addEventListener("change", render);
    return () => {
      ++generation.current;
      clearTimeout(timer);
      observer.disconnect();
      media.removeEventListener("change", render);
    };
  }, [source, loaded, expanded]);
  const visible = state === "ready" && !showSource;
  const zoom = (action: string) =>
    frame.current?.contentWindow?.postMessage(
      { type: "spool:zoom-mermaid", action },
      "*",
    );
  return (
    <div className="markdown-mermaid" data-ready={visible}>
      {html && (
        <iframe
          ref={frame}
          className="markdown-mermaid-frame"
          title="Mermaid"
          sandbox="allow-scripts"
          srcDoc={html}
          style={{ height: expanded ? "100%" : height }}
          aria-hidden={!visible}
          onLoad={() => setLoaded(true)}
        />
      )}
      {!visible && (showSource || !expanded || state === "error") && children}
      {expanded && !showSource && state === "loading" && (
        <div className="mermaid-loading" role="status">
          {t("Rendering diagram…")}
        </div>
      )}
      {expanded && state === "ready" && (
        <div className="mermaid-zoom-toolbar">
          <button
            type="button"
            className="icon"
            aria-label={t("Zoom out")}
            title={t("Zoom out")}
            onClick={() => zoom("out")}
          >
            <Minus size={18} />
          </button>
          <button
            type="button"
            className="icon"
            aria-label={t("Fit diagram")}
            title={t("Fit diagram")}
            onClick={() => zoom("fit")}
          >
            <Scan size={18} />
          </button>
          <button
            type="button"
            className="icon"
            aria-label={t("Zoom in")}
            title={t("Zoom in")}
            onClick={() => zoom("in")}
          >
            <Plus size={18} />
          </button>
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
  useEffect(() => {
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
