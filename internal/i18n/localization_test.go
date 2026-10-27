package i18n

import (
	"strings"
	"testing"
)

func TestLocalizationCatalogs(t *testing.T) {
	for locale, catalog := range messages {
		for key := range messages["en"] {
			if strings.TrimSpace(catalog[key]) == "" {
				t.Errorf("%s has no translation for %q", locale, key)
			}
		}
	}
	if ResolveLanguage("system", "fr-FR", "ja-JP", "en") != "ja" || ResolveLanguage("zh-CN", "en") != "zh-CN" {
		t.Fatal("language resolution is incorrect")
	}
	if Translate("ja", "{count} online", "count", "3") != "3 人がオンライン" {
		t.Fatal("translation interpolation failed")
	}
}
