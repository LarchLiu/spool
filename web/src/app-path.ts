// Public relay routes mount the same application below one stable server path.
// Never use a routing cookie: two tabs can connect to different servers.
export function appBasePath(pathname = globalThis.location?.pathname ?? "/"): string {
  return pathname.match(/^\/web\/[a-z2-7]{52}\//)?.[0] ?? "/";
}

export function appURL(path: string): string {
  const base = appBasePath();
  if (!path.startsWith("/") || path.startsWith("//") || path.startsWith(base) && base !== "/") return path;
  return base + path.slice(1);
}

// Server JSON keeps canonical /api URLs for native and direct clients.
export function apiPath(path: string): string {
  const base = appBasePath();
  if (base !== "/" && path.startsWith(base)) path = "/" + path.slice(base.length);
  return path.startsWith("/api/") ? path.slice(4) : path;
}
