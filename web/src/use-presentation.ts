import { useEffect, useMemo, useState } from "react";
import { setAPILanguage } from "./api";
import { translator } from "./i18n";
import {
  resolveLanguage,
  resolveTheme,
  type UIPreferences,
} from "./preferences";

export function usePresentation(preferences: UIPreferences) {
  const [systemDark, setSystemDark] = useState(
    () => matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const [systemLanguages, setSystemLanguages] = useState(
    () => navigator.languages,
  );
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const themeChanged = () => setSystemDark(media.matches);
    const languageChanged = () => setSystemLanguages([...navigator.languages]);
    media.addEventListener("change", themeChanged);
    window.addEventListener("languagechange", languageChanged);
    return () => {
      media.removeEventListener("change", themeChanged);
      window.removeEventListener("languagechange", languageChanged);
    };
  }, []);
  const locale = resolveLanguage(preferences.language, systemLanguages);
  const theme = resolveTheme(preferences.theme, systemDark);
  const t = useMemo(() => translator(locale), [locale]);
  // Updating the request locale during render also covers requests from sibling
  // effects in the same frame, rather than waiting for a later effect.
  setAPILanguage(locale);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.lang = locale;
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute("content", theme === "dark" ? "#1e1e1e" : "#f6f6f3");
  }, [theme, locale]);
  return { locale, theme, t };
}
