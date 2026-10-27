import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolveLanguage, resolveTheme } from "../.test-build/preferences.js";

test("theme follows the OS only when system is selected", () => {
  assert.equal(resolveTheme("system", true), "dark");
  assert.equal(resolveTheme("system", false), "light");
  assert.equal(resolveTheme("light", true), "light");
  assert.equal(resolveTheme("dark", false), "dark");
});

test("locale respects explicit choice and ordered system languages", () => {
  assert.equal(resolveLanguage("ja", ["en-US"]), "ja");
  assert.equal(
    resolveLanguage("system", ["fr-FR", "zh-Hans-CN", "en"]),
    "zh-CN",
  );
  assert.equal(resolveLanguage("system", ["en-GB", "ja-JP"]), "en");
  assert.equal(resolveLanguage("system", ["de-DE"]), "en");
});

test("all shared locale catalogs have matching keys and placeholders", () => {
  const read = (locale) =>
    JSON.parse(
      readFileSync(new URL(`../../locales/${locale}.json`, import.meta.url)),
    );
  const en = read("en");
  const parameters = (text) =>
    [...text.matchAll(/\{([^}]+)\}/g)].map((m) => m[1]).sort();
  for (const locale of ["zh-CN", "ja"]) {
    const catalog = read(locale);
    assert.deepEqual(Object.keys(catalog).sort(), Object.keys(en).sort());
    for (const [key, translation] of Object.entries(catalog)) {
      assert.ok(translation.trim(), `${locale} ${key}`);
      assert.deepEqual(
        parameters(translation),
        parameters(en[key]),
        `${locale} ${key}`,
      );
    }
  }
});
