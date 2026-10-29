package main

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"

	"spool/internal/workspace"
)

func TestNativeServiceFieldsAutosaveOnBlurEnterAndNavigation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPOOL_CODEX_BIN", filepath.Join(root, "no-codex"))
	t.Setenv("SPOOL_CLAUDE_BIN", filepath.Join(root, "no-claude"))
	service, err := workspace.OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	p := &panel{service: service, settings: workspace.DefaultSettings(), port: "8787", tab: "Service", preferences: workspace.UIPreferences{Language: "en"}}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			p.serviceSettingField(c, "Host", &p.settings.Host, func(s *workspace.Settings, value string) error { s.Host = value; return nil })
			p.serviceSettingField(c, "Port", &p.port, func(s *workspace.Settings, value string) (err error) { s.Port, err = strconv.Atoi(value); return err })
		})
	}, 600, 400)
	replace := func(label, text string) {
		t.Helper()
		for i := 0; i < 3 && !view.Focused(label); i++ {
			view.Key(0, ui.KeyTab)
		}
		if !view.Focused(label) {
			t.Fatal("could not focus the input", label)
		}
		view.Command("selectAll")
		view.Type(text)
	}
	replace("Host", "localhost")
	if settings, _ := service.Store.Settings(); settings.Host != "127.0.0.1" {
		t.Fatal("typing must not save a partial field", settings)
	}
	replace("Port", "9000")
	if settings, _ := service.Store.Settings(); settings.Host != "localhost" || settings.Port != 8787 || p.busy {
		t.Fatal("blur failed to save just the previous field", settings, p.busy)
	}
	view.Key(0, ui.KeyEnter)
	if settings, _ := service.Store.Settings(); settings.Host != "localhost" || settings.Port != 9000 {
		t.Fatal("Enter failed to merge the port into saved settings", settings)
	}
	replace("Host", "0.0.0.0")
	p.selectTab("People")
	if settings, _ := service.Store.Settings(); settings.Host != "0.0.0.0" || settings.Port != 9000 {
		t.Fatal("navigation dropped the focused input", settings)
	}
	if service.Status().Running {
		t.Fatal("autosave must not start or restart the service")
	}
}
