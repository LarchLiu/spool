const CACHE = "spool-chat-shell-v2";
self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") event.waitUntil(self.skipWaiting());
});
self.addEventListener("install", (event) =>
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      await cache.addAll([
        "/offline.html",
        "/offline.js",
        "/icon-192.png",
        "/icon-512.png",
        "/icon-maskable-512.png",
        "/apple-touch-icon.png",
        "/favicon.png",
        "/logo.png",
      ]);
    })(),
  ),
);
self.addEventListener("activate", (event) =>
  event.waitUntil(
    (async () => {
      for (const key of await caches.keys())
        if (
          (key.startsWith("spool-chat-shell-") ||
            key.startsWith("tern-chat-shell-")) &&
          key !== CACHE
        )
          await caches.delete(key);
      await self.clients.claim();
    })(),
  ),
);
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  // Credentials, chat data, login responses and SSE never enter Cache Storage.
  if (
    url.origin !== self.location.origin ||
    event.request.method !== "GET" ||
    url.pathname.startsWith("/api/") ||
    url.pathname === "/sw.js"
  )
    return;
  if (event.request.mode === "navigate") {
    event.respondWith(
      fetch(event.request).catch(() => caches.match("/offline.html")),
    );
  } else if (
    url.pathname.startsWith("/assets/") ||
    url.pathname === "/offline.js" ||
    /^\/(?:icon-(?:maskable-)?\d+|apple-touch-icon|favicon|logo)\.png$/.test(
      url.pathname,
    )
  ) {
    event.respondWith(
      (async () => {
        const cache = await caches.open(CACHE);
        const hit = await cache.match(event.request);
        if (hit) return hit;
        const response = await fetch(event.request);
        if (response.ok && response.type === "basic")
          await cache.put(event.request, response.clone());
        return response;
      })(),
    );
  }
});
