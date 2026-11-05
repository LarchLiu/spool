import { getCSRF } from "./api";

// voxl owns its viewer controls. Keep those controls reachable inside an iframe
// and attach Spool's existing CSRF token to its native upload requests.
export function configureVoxlFrame(frame: HTMLIFrameElement) {
  const doc = frame.contentDocument;
  const view = doc?.defaultView;
  if (!doc || !view || doc.getElementById("spool-voxl-controls")) return;
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
  xhr.open = function (method: string, url: string | URL, ...args: unknown[]) {
    const target = new URL(String(url), doc.baseURI);
    if (
      method.toUpperCase() === "PUT" &&
      target.origin === view.location.origin &&
      target.pathname === "/api/desktop/view/files/upload"
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
