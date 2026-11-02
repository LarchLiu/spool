import { translator } from "./i18n";
import { resolveLanguage, type Locale } from "./preferences";
import { setToolDetailSource, toolDetailCache } from "./tool-detail.js";

setToolDetailSource((path, signal) => api(path, undefined, signal));

let csrf = "";
let language = resolveLanguage("system", navigator.languages);
export function setAPILanguage(locale: Locale) {
  language = locale;
}
export function setCSRF(value: string) {
  if (csrf !== value) toolDetailCache.clear();
  csrf = value;
}
export function getCSRF() {
  return csrf;
}
export async function api<T>(
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const multipart = body instanceof FormData;
  const lazyPath = path.startsWith("/chats/") && !path.includes("/tool-detail?")
    ? path + (path.includes("?") ? "&" : "?") + "tool_details=lazy" : path;
  const response = await fetch("/api" + lazyPath, {
    method: body === undefined ? "GET" : "POST",
    credentials: "same-origin",
    cache: "no-store",
    signal,
    headers: {
      "X-UI-Language": language,
      ...(body === undefined
        ? {}
        : {
            ...(!multipart && { "Content-Type": "application/json" }),
            "X-CSRF-Token": csrf,
          }),
    },
    body:
      body === undefined ? undefined : multipart ? body : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({
    error: translator(language)("The server returned an invalid response"),
  }));
  if (!response.ok) {
    if (response.status === 401 && path !== "/login")
      window.dispatchEvent(new Event("auth-expired"));
    throw new Error(
      data.error ||
        `${translator(language)("Request failed")} (${response.status})`,
    );
  }
  return data as T;
}
