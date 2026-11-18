import { appBasePath, appURL } from "./app-path";
import type { MessageKey } from "./i18n";

export type ImageResource = { name: string; url: string; downloadURL: string };
const maxInlineLength = Math.ceil((16 * 1024 * 1024) / 3) * 4 + 64;

// Canonical service URLs, inline raster pixels and owned draft blobs are the
// only image sources. Provider output cannot cause an arbitrary network read.
export function imageSource(source: string, chatID?: string): string | undefined {
  if (/^data:image\/(?:png|jpeg|gif|webp);base64,/.test(source))
    return source.length <= maxInlineLength ? source : undefined;
  if (source.startsWith("blob:")) return source;
  const base = appBasePath();
  if (base !== "/" && source.startsWith(base)) source = "/" + source.slice(base.length);
  if (!/^\/api\/(?:chats|projects)\/[^/?#]+\/(?:file|attachments|tool-image)\?/.test(source)) return;
  const url = new URL(source, "http://spool.invalid");
  if (url.hash || (chatID !== undefined && !url.pathname.startsWith(`/api/chats/${encodeURIComponent(chatID)}/`))) return;
  const query = url.searchParams;
  if ([...query.keys()].some(key => query.getAll(key).length !== 1)) return;
  const only = (...keys: string[]) => [...query.keys()].every(key => keys.includes(key));
  if (url.pathname.endsWith("/tool-image")) {
    if (!only("ref", "index", "download") || !/^[a-zA-Z0-9_-]{1,16400}$/.test(query.get("ref") || "") || !/^\d+$/.test(query.get("index") || "")) return;
  } else if (url.pathname.endsWith("/attachments")) {
    if (!only("id", "download") || !query.get("id")) return;
  } else if (!only("path", "image", "download", "workspace") || !query.get("path") || query.get("image") !== "1" && query.get("download") !== "1") return;
  if (query.has("download") && query.get("download") !== "1") return;
  return appURL(source);
}

export function imageResource(name: string, source: string, download?: string): ImageResource | undefined {
  const url = imageSource(source);
  if (!url) return;
  return { name, url, downloadURL: download ? imageSource(download) || url : url };
}

export function imageFailureMessage(status: number): MessageKey {
  if (status === 404 || status === 410) return "File not found";
  if (status === 401 || status === 403) return "Image access denied";
  if (status >= 400 && status < 500) return "This file cannot be previewed as an image";
  return "Image could not be loaded. Try again.";
}
