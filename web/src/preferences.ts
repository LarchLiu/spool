export type Theme = "system" | "light" | "dark";
export type Language = "system" | "en" | "zh-CN" | "ja";
export type Locale = Exclude<Language, "system">;
export type UIPreferences = { theme: Theme; language: Language };
export const defaultPreferences: UIPreferences = {
  theme: "system",
  language: "system",
};
export const preferencesStorageKey = "spool.ui-preferences.v1";

export function parsePreferences(raw: string | null): UIPreferences {
  try {
    const value = JSON.parse(raw || "null");
    return {
      theme: ["system", "light", "dark"].includes(value?.theme) ? value.theme : "system",
      language: ["system", "zh-CN", "en", "ja"].includes(value?.language) ? value.language : "system",
    };
  } catch {
    return { ...defaultPreferences };
  }
}
export const languages = [
  ["zh-CN", "简体中文"],
  ["en", "English"],
  ["ja", "日本語"],
] as const;

export function resolveLanguage(
  preference: Language,
  preferred: readonly string[],
): Locale {
  const candidates = preference === "system" ? preferred : [preference];
  for (const candidate of candidates) {
    const base = candidate.toLowerCase().replaceAll("_", "-").split("-")[0];
    if (base === "zh") return "zh-CN";
    if (base === "en" || base === "ja") return base;
  }
  return "en";
}

export function resolveTheme(
  theme: Theme,
  systemDark: boolean,
): "light" | "dark" {
  return theme === "system" ? (systemDark ? "dark" : "light") : theme;
}
