const CACHE = "spool-chat-shell-v2";
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
      let target = new URL("/", self.location.origin);
      try {
        const url = new URL(data?.url || "/", self.location.origin);
        if (url.origin === self.location.origin && url.pathname === "/")
          target = url;
      } catch {}
      const title =
        typeof data?.title === "string" && data.title.trim()
          ? Array.from(data.title.trim()).slice(0, 160).join("")
          : "Spool";
      await self.registration.showNotification(title, {
        body:
          typeof data?.body === "string"
            ? data.body.slice(0, 300)
            : "Open Spool to view your task.",
        icon: "/icon-192.png",
        badge: "/favicon.png",
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
      let target = new URL("/", self.location.origin);
      try {
        const url = new URL(
          event.notification.data?.url || "/",
          self.location.origin,
        );
        if (url.origin === self.location.origin && url.pathname === "/")
          target = url;
      } catch {}
      const windows = await self.clients.matchAll({ type: "window" });
      for (const client of windows) {
        const url = new URL(client.url);
        if (url.origin !== self.location.origin || url.pathname !== "/")
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
