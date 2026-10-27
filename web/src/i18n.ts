import en from "../../locales/en.json";
import zh from "../../locales/zh-CN.json";
import ja from "../../locales/ja.json";
import type { Locale } from "./preferences";

export type MessageKey = keyof typeof en;
export type Translator = (
  key: MessageKey,
  values?: Record<string, string | number>,
) => string;
const catalogs = { en, "zh-CN": zh, ja };

export function translator(locale: Locale): Translator {
  return (key, values = {}) => {
    const message: string = catalogs[locale][key] || en[key];
    return message.replace(/\{([^}]+)\}/g, (match, name: string) =>
      values[name] === undefined ? match : String(values[name]),
    );
  };
}

export function effortLabel(value: string, t: Translator): string {
  const keys: Record<string, MessageKey> = {
    none: "None",
    minimal: "Minimal",
    low: "Low",
    medium: "Medium",
    high: "High",
    xhigh: "Extra High",
    max: "Max",
  };
  return !value ? t("Default") : keys[value] ? t(keys[value]) : value;
}
