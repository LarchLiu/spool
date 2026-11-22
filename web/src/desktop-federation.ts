import { appURL } from "./app-path";

export type FederationTarget = { id: string; name: string; clientURL?: string };

// 只用于匹配已有标签；实际登录仍由 Desktop 原生端重新核验。
export function federationClientURL(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 2048) return;
  try {
    const url = new URL(value);
    if (
      url.protocol !== "https:" ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    )
      return;
    url.pathname = url.pathname.replace(/\/+$/, "") + "/";
    return url.href;
  } catch {
    return;
  }
}

// Servers this signed-in account may enter with its current identity. The
// list is display data; the desktop decides how each one may be reached.
export async function loadFederationTargets(
  signal?: AbortSignal,
  request: typeof fetch = fetch,
): Promise<FederationTarget[]> {
  const response = await request(appURL("/api/federation/targets"), {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    signal,
  });
  if (!response.ok) return [];
  const value: unknown = await response.json().catch(() => null);
  if (!Array.isArray(value)) return [];
  const targets: FederationTarget[] = [];
  for (const item of value.slice(0, 64)) {
    if (
      typeof item !== "object" ||
      item === null ||
      typeof item.id !== "string" ||
      !/^[a-z2-7]{52}$/.test(item.id) ||
      typeof item.name !== "string" ||
      targets.some((target) => target.id === item.id)
    )
      continue;
    targets.push({
      id: item.id,
      name: item.name.trim().slice(0, 80) || item.id.slice(0, 8),
      clientURL: federationClientURL(
        "client_url" in item ? item.client_url : undefined,
      ),
    });
  }
  return targets;
}
