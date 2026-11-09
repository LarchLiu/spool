const BASE = new URL(self.registration.scope).pathname;
const at = (path) => BASE + path.replace(/^\//, "");
const CACHE_PREFIX = "spool-chat-shell:" + BASE + ":";
const CACHE = CACHE_PREFIX + "v3";
function notificationURL(value) {
  const target = new URL(BASE, self.location.origin);
  try {
    const url = new URL(value || "/", self.location.origin);
    if (url.origin === self.location.origin && (url.pathname === "/" || url.pathname === BASE)) {
      target.search = url.search;
    }
  } catch {}
  return target;
}
self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") event.waitUntil(self.skipWaiting());
});
self.addEventListener("push", (event) => {
  event.waitUntil(
    (async () => {
      let data;
      try {
        data = event.data?.json();
      } catch {
        // Even an empty or malformed push must produce a visible notification.
      }
      const target = notificationURL(data?.url);
      const title =
        typeof data?.title === "string" && data.title.trim()
          ? Array.from(data.title.trim()).slice(0, 160).join("")
          : "Spool";
      await self.registration.showNotification(title, {
        body:
          typeof data?.body === "string"
            ? data.body.slice(0, 300)
            : "Open Spool to view your task.",
        icon: at("/icon-192.png"),
        badge: at("/favicon.png"),
        tag:
          typeof data?.tag === "string" ? data.tag.slice(0, 200) : "spool-task",
        data: { url: target.href },
      });
    })(),
  );
});
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const target = notificationURL(event.notification.data?.url);
      const windows = await self.clients.matchAll({ type: "window" });
      for (const client of windows) {
        const url = new URL(client.url);
        if (url.origin !== self.location.origin || url.pathname !== BASE)
          continue;
        await client.focus();
        const chatID = target.searchParams.get("chat");
        const peerID = target.searchParams.get("inbox");
        const userID = target.searchParams.get("recipient");
        const message =
          peerID && userID
            ? { type: "OPEN_INBOX", peerID, userID }
            : chatID
              ? { type: "OPEN_CHAT", chatID }
              : null;
        if (message) {
          const handled = await new Promise((resolve) => {
            const channel = new MessageChannel();
            const finish = (value) => {
              clearTimeout(timeout);
              channel.port1.close();
              channel.port2.close();
              resolve(value);
            };
            const timeout = setTimeout(() => finish(false), 1500);
            channel.port1.onmessage = () => finish(true);
            try {
              client.postMessage(message, [channel.port2]);
            } catch {
              finish(false);
            }
          });
          // Old app versions and the offline page have no message listener.
          if (!handled) await client.navigate(target.href);
        }
        return;
      }
      await self.clients.openWindow(target.href);
    })(),
  );
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
      ].map(at));
    })(),
  ),
);
self.addEventListener("activate", (event) =>
  event.waitUntil(
    (async () => {
      for (const key of await caches.keys())
        if (
          (key.startsWith(CACHE_PREFIX) ||
            BASE === "/" && (key.startsWith("spool-chat-shell-") || key.startsWith("tern-chat-shell-"))) &&
          key !== CACHE
        )
          await caches.delete(key);
      await self.clients.claim();
    })(),
  ),
);
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  const path = "/" + url.pathname.slice(BASE.length);
  // Credentials, chat data, login responses and SSE never enter Cache Storage.
  if (
    url.origin !== self.location.origin ||
    !url.pathname.startsWith(BASE) ||
    event.request.method !== "GET" ||
    path.startsWith("/api/") ||
    path === "/sw.js"
  )
    return;
  if (event.request.mode === "navigate") {
    event.respondWith(
      (async () => {
        try {
          const response = await fetch(event.request);
          if (response.status !== 502 && response.status !== 503) return response;
          return (await caches.open(CACHE).then((cache) => cache.match(at("/offline.html")))) || response;
        } catch {
          return caches.open(CACHE).then((cache) => cache.match(at("/offline.html")));
        }
      })(),
    );
  } else if (
    path.startsWith("/assets/") ||
    path === "/offline.js" ||
    /^\/(?:icon-(?:maskable-)?\d+|apple-touch-icon|favicon|logo)\.png$/.test(
      path,
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
