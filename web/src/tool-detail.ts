export type ToolDetailCursor = { offset: number; prefix: string };
export type ToolDetailPage = {
  ref: string;
  version: string;
  text: string;
  offset: number;
  next: number;
  total: number;
  prefix: string;
  reset?: boolean;
};
export const toolDetailPageBytes = 32 * 1024;
export const firstToolDetailCursor: ToolDetailCursor = { offset: 0, prefix: "" };

// Keep at most one display page per mounted body. Large outputs never become a
// multi-megabyte Markdown/diff tree, even when a user expands a running tool.
export function mergeToolDetailPage(previous: ToolDetailPage | undefined, incoming: ToolDetailPage): ToolDetailPage {
  if (!previous || incoming.reset || incoming.offset !== previous.next) return incoming;
  if (incoming.next - previous.offset > toolDetailPageBytes) {
    return { ...previous, total: incoming.total, version: incoming.version };
  }
  return { ...incoming, offset: previous.offset, text: previous.text + incoming.text };
}

export class ToolDetailCache {
  private pages = new Map<string, ToolDetailPage>();
  private bytes = 0;
  private epoch = 0;
  constructor(private readonly maxBytes = 8 * 1024 * 1024, private readonly maxPages = 128) {}
  clear() { this.epoch++; this.pages.clear(); this.bytes = 0; }
  async load(
    source: (path: string, signal: AbortSignal) => Promise<ToolDetailPage>,
    href: string, version: string, cursor: ToolDetailCursor, signal: AbortSignal,
  ): Promise<ToolDetailPage> {
    const key = JSON.stringify([href, version, cursor.offset, cursor.prefix]);
    const cached = this.pages.get(key);
    if (cached) { this.pages.delete(key); this.pages.set(key, cached); return cached; }
    const epoch = this.epoch;
    const page = await source(href + "&offset=" + cursor.offset + "&prefix=" + encodeURIComponent(cursor.prefix), signal);
    if (signal.aborted || epoch !== this.epoch) return page;
    const size = page.text.length * 2 + key.length * 2 + 256;
    if (size <= this.maxBytes) {
      const old = this.pages.get(key);
      if (old) this.bytes -= old.text.length * 2 + key.length * 2 + 256;
      this.pages.set(key, page); this.bytes += size;
      while (this.pages.size > this.maxPages || this.bytes > this.maxBytes) {
        const [oldKey, oldPage] = this.pages.entries().next().value!;
        this.bytes -= oldPage.text.length * 2 + oldKey.length * 2 + 256;
        this.pages.delete(oldKey);
      }
    }
    return page;
  }
}

export const toolDetailCache = new ToolDetailCache();
let detailSource: ((path: string, signal: AbortSignal) => Promise<ToolDetailPage>) | undefined;
export function setToolDetailSource(source: (path: string, signal: AbortSignal) => Promise<ToolDetailPage>) { detailSource = source; }
export function requestToolDetail(path: string, signal: AbortSignal): Promise<ToolDetailPage> {
  if (!detailSource) return Promise.reject(new Error("Tool detail source is unavailable"));
  return detailSource(path, signal);
}
