import { appURL } from "./app-path";
import { getCSRF } from "./api";
import type { Translator } from "./i18n";
import { setupVoxlZoom } from "./voxl-zoom";

const viewerDisposers = new WeakMap<Document, () => void>();

// voxl owns its viewer controls. Keep those controls reachable inside an iframe
// and attach Spool's existing CSRF token to its native upload requests.
export function configureVoxlFrame(
  frame: HTMLIFrameElement,
  t: Translator = (key) => key,
) {
  const doc = frame.contentDocument;
  const view = doc?.defaultView;
  if (!doc || !view) return;
  const active = viewerDisposers.get(doc);
  if (active) return active;
  if (!doc.getElementById("spool-voxl-controls")) {
    const style = doc.createElement("style");
    style.id = "spool-voxl-controls";
    style.textContent = `
    #tools { top: auto !important; bottom: 12px; right: 12px; opacity: 1 !important; pointer-events: auto !important; }
    #tools #kb-btn { display: inline-grid !important; }
    #tools.read-only #kb-btn { display: none !important; }
  `;
    doc.head.append(style);

    const xhr = (view as Window & typeof globalThis).XMLHttpRequest.prototype;
    const open = xhr.open;
    const send = xhr.send;
    const uploads = new WeakSet<XMLHttpRequest>();
    xhr.open = function (
      method: string,
      url: string | URL,
      ...args: unknown[]
    ) {
      const target = new URL(String(url), doc.baseURI);
      if (
        method.toUpperCase() === "PUT" &&
        target.origin === view.location.origin &&
        target.pathname === appURL("/api/desktop/view/files/upload")
      ) {
        uploads.add(this);
      } else uploads.delete(this);
      return Reflect.apply(open, this, [method, url, ...args]);
    };
    xhr.send = function (body?: Document | XMLHttpRequestBodyInit | null) {
      if (uploads.has(this)) this.setRequestHeader("X-CSRF-Token", getCSRF());
      return send.call(this, body);
    };
  }
  const cleanup = setupVoxlZoom(doc, t);
  const dispose = () => {
    cleanup();
    if (viewerDisposers.get(doc) === dispose) viewerDisposers.delete(doc);
  };
  viewerDisposers.set(doc, dispose);
  return dispose;
}
