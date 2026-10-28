import {
  resolveLanguage,
  type Locale,
  type UIPreferences,
} from "./preferences.js";

// A presentation-only hint injected by the desktop's page preload. It must
// never be used for authentication or to grant API/native privileges.
export function isDesktopChat(
  surface: unknown = globalThis,
): surface is { spoolDesktop: true } {
  return (
    typeof surface === "object" &&
    surface !== null &&
    "spoolDesktop" in surface &&
    surface.spoolDesktop === true
  );
}

export const desktopSettingsPath = "/__desktop/settings";

export function isMacDesktop(surface: unknown = globalThis): boolean {
  return (
    isDesktopChat(surface) &&
    "mygo" in surface &&
    typeof surface.mygo === "object" &&
    surface.mygo !== null &&
    "platform" in surface.mygo &&
    surface.mygo.platform === "darwin"
  );
}

type DesktopRuntime = {
  readonly locale: string;
  on: (event: string, listener: () => void) => () => void;
};

function desktopRuntime(surface: unknown): DesktopRuntime | undefined {
  if (!isDesktopChat(surface) || !("mygo" in surface)) return;
  const runtime = surface.mygo;
  if (
    typeof runtime === "object" &&
    runtime !== null &&
    "locale" in runtime &&
    typeof runtime.locale === "string" &&
    "on" in runtime &&
    typeof runtime.on === "function"
  ) {
    return runtime as DesktopRuntime;
  }
}

export function desktopLocale(
  surface: unknown = globalThis,
): Locale | undefined {
  const runtime = desktopRuntime(surface);
  return runtime ? resolveLanguage("system", [runtime.locale]) : undefined;
}

export function desktopTheme(
  surface: unknown = globalThis,
): "light" | "dark" | undefined {
  if (!isDesktopChat(surface) || !("spoolDesktopTheme" in surface)) return;
  const theme = surface.spoolDesktopTheme;
  return theme === "light" || theme === "dark" ? theme : undefined;
}

export function subscribeDesktopTheme(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  if (
    !isDesktopChat(surface) ||
    !("addEventListener" in surface) ||
    typeof surface.addEventListener !== "function" ||
    !("removeEventListener" in surface) ||
    typeof surface.removeEventListener !== "function"
  ) {
    return () => {};
  }
  const removeEventListener = surface.removeEventListener.bind(surface);
  surface.addEventListener("spool:appearance-changed", listener);
  return () => removeEventListener("spool:appearance-changed", listener);
}

export function subscribeDesktopLocale(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return desktopRuntime(surface)?.on("mygo:locale", listener) ?? (() => {});
}

export function chatPreferences(
  preferences: UIPreferences,
  locale: Locale | undefined,
  surface: unknown = globalThis,
): UIPreferences {
  if (!isDesktopChat(surface)) return preferences;
  // MyGo makes prefers-color-scheme reflect the native theme override, not
  // just the OS setting. Account preferences must not override desktop UI.
  return { theme: "system", language: locale ?? "system" };
}
