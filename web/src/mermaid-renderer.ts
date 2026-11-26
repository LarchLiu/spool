/// <reference types="vite/client" />
export type MermaidVector =
  | {
      state: "ready";
      url: string;
      width: number;
      height: number;
      title?: string;
      description?: string;
    }
  | { state: "error" };

const cache = new Map<string, { value: MermaidVector; cost: number }>();
const pending = new Map<string, Job>();
const queue: Job[] = [];
const maxVector = 1024 * 1024;
let cacheSize = 0,
  working = false,
  revision = 0;
let documentPromise: Promise<string> | undefined;

type Consumer = { resolve: (value: MermaidVector) => void; cancel: () => void };
type Job = {
  key: string;
  source: string;
  dark: boolean;
  abort: AbortController;
  consumers: Set<Consumer>;
};
const identity = (source: string, dark: boolean) =>
  `${dark ? "dark" : "light"}:${source}`;
const aborted = () =>
  new DOMException("Diagram no longer requested", "AbortError");

export function cachedMermaid(
  source: string,
  dark: boolean,
): MermaidVector | undefined {
  const key = identity(source, dark),
    entry = cache.get(key);
  if (entry) {
    cache.delete(key);
    cache.set(key, entry);
  }
  return entry?.value;
}

function remember(key: string, value: MermaidVector) {
  const cost =
    key.length * 2 + (value.state === "ready" ? value.url.length * 2 : 256);
  if (cost > 8 * 1024 * 1024) return;
  const previous = cache.get(key);
  if (previous) {
    cacheSize -= previous.cost;
    cache.delete(key);
  }
  cache.set(key, { value, cost });
  cacheSize += cost;
  while (cache.size > 32 || cacheSize > 8 * 1024 * 1024) {
    const oldest = cache.keys().next().value!;
    cacheSize -= cache.get(oldest)!.cost;
    cache.delete(oldest);
  }
}

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

// Keep untrusted SVG out of the app DOM. SVG image mode also disables scripts,
// links and external resources, even after the compiler iframe has been released.
function vector(document: string): MermaidVector {
  if (document.length > maxVector || /<!doctype|<!entity/i.test(document))
    throw new Error("Invalid diagram output");
  const svg = new DOMParser().parseFromString(document, "image/svg+xml");
  const root = svg.documentElement;
  if (
    root.localName !== "svg" ||
    root.namespaceURI !== "http://www.w3.org/2000/svg" ||
    svg.querySelector("parsererror,script,foreignObject,image,style")
  )
    throw new Error("Invalid diagram output");
  for (const element of [root, ...root.querySelectorAll("*")])
    for (const attribute of element.attributes) {
      if (
        attribute.localName === "href" &&
        !attribute.value.trim().startsWith("#")
      )
        throw new Error("External diagram reference");
      for (const match of attribute.value.matchAll(
        /url\(\s*["']?([^)'"\s]*)["']?\s*\)/gi,
      ))
        if (!match[1].startsWith("#"))
          throw new Error("External diagram reference");
    }
  const width = Number(root.getAttribute("width")),
    height = Number(root.getAttribute("height"));
  if (
    ![width, height].every(
      (value) => Number.isFinite(value) && value > 0 && value <= 20000,
    )
  )
    throw new Error("Invalid diagram size");
  return {
    state: "ready",
    url: `data:image/svg+xml;charset=utf-8,${encodeURIComponent(document)}`,
    width,
    height,
    title:
      root.querySelector(":scope > title")?.textContent?.trim() || undefined,
    description:
      root.querySelector(":scope > desc")?.textContent?.trim() || undefined,
  };
}

function load(frame: HTMLIFrameElement, html: string, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const finish = (error?: unknown) => {
      clearTimeout(timer);
      frame.removeEventListener("load", ready);
      signal.removeEventListener("abort", cancel);
      if (error) reject(error);
      else resolve();
    };
    const ready = () => finish(),
      cancel = () => finish(aborted());
    const timer = setTimeout(
      () => finish(new Error("Diagram compiler timed out")),
      15000,
    );
    frame.addEventListener("load", ready);
    signal.addEventListener("abort", cancel, { once: true });
    frame.srcdoc = html;
    document.body.appendChild(frame);
    if (signal.aborted) cancel();
  });
}

function compile(frame: HTMLIFrameElement, job: Job) {
  return new Promise<MermaidVector>((resolve, reject) => {
    const generation = ++revision;
    const finish = (value?: MermaidVector, error?: unknown) => {
      clearTimeout(timer);
      window.removeEventListener("message", receive);
      job.abort.signal.removeEventListener("abort", cancel);
      if (error) reject(error);
      else resolve(value!);
    };
    const receive = (event: MessageEvent) => {
      const data = event.data;
      if (
        event.source !== frame.contentWindow ||
        data?.type !== "spool:mermaid" ||
        data.generation !== generation
      )
        return;
      if (data.ready === false) finish({ state: "error" });
      else if (data.ready === true && typeof data.svg === "string") {
        try {
          finish(vector(data.svg));
        } catch (error) {
          finish(undefined, error);
        }
      }
    };
    const cancel = () => finish(undefined, aborted());
    const timer = setTimeout(
      () => finish(undefined, new Error("Diagram rendering timed out")),
      20000,
    );
    window.addEventListener("message", receive);
    job.abort.signal.addEventListener("abort", cancel, { once: true });
    frame.contentWindow?.postMessage(
      {
        type: "spool:render-mermaid",
        source: job.source,
        dark: job.dark,
        generation,
        snapshot: true,
      },
      "*",
    );
    if (job.abort.signal.aborted) cancel();
  });
}

async function drain() {
  if (working) return;
  working = true;
  let frame: HTMLIFrameElement | undefined;
  try {
    for (let job; (job = queue.shift());) {
      if (job.abort.signal.aborted) {
        if (pending.get(job.key) === job) pending.delete(job.key);
        continue;
      }
      let value: MermaidVector = { state: "error" };
      try {
        const html = await loadMermaidDocument();
        if (job.abort.signal.aborted) continue;
        if (!frame) {
          frame = document.createElement("iframe");
          frame.className = "mermaid-compiler";
          frame.title = "Mermaid compiler";
          frame.setAttribute("sandbox", "allow-scripts");
          frame.setAttribute("aria-hidden", "true");
          frame.tabIndex = -1;
          await load(frame, html, job.abort.signal);
        }
        value = await compile(frame, job);
        if (!job.abort.signal.aborted) remember(job.key, value);
      } catch {
        // A canceled or failed compiler must not block later diagrams. Transport
        // and timeout failures are not cached as source errors.
        frame?.remove();
        frame = undefined;
      } finally {
        if (pending.get(job.key) === job) pending.delete(job.key);
        for (const consumer of job.consumers) {
          consumer.cancel();
          consumer.resolve(value);
        }
        job.consumers.clear();
      }
    }
  } finally {
    frame?.remove();
    working = false;
  }
}

/** Deduplicate callers and serialize first renders in one temporary sandbox. */
export function renderMermaid(
  source: string,
  dark: boolean,
  signal: AbortSignal,
): Promise<MermaidVector> {
  if (signal.aborted) return Promise.reject(aborted());
  const cached = cachedMermaid(source, dark);
  if (cached) return Promise.resolve(cached);
  const key = identity(source, dark);
  let job = pending.get(key);
  if (!job) {
    job = {
      key,
      source,
      dark,
      abort: new AbortController(),
      consumers: new Set(),
    };
    pending.set(key, job);
    queue.push(job);
  }
  const current = job;
  const promise = new Promise<MermaidVector>((resolve, reject) => {
    const cancel = () => {
      current.consumers.delete(consumer);
      signal.removeEventListener("abort", cancel);
      reject(aborted());
      if (!current.consumers.size) {
        if (pending.get(key) === current) pending.delete(key);
        current.abort.abort();
      }
    };
    const consumer = {
      resolve,
      cancel: () => signal.removeEventListener("abort", cancel),
    };
    current.consumers.add(consumer);
    signal.addEventListener("abort", cancel, { once: true });
  });
  void drain();
  return promise;
}
