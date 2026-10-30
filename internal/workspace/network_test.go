package workspace

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spool/internal/netproxy"
	"spool/internal/tailnet"
)

func TestCustomProxySavesAndAppliesWithoutInterruptingService(t *testing.T) {
	service, settings := settingsService(t)
	app, runner := service.app, service.runner
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "spool-custom-proxy.invalid" {
			t.Errorf("unexpected proxy target %q", r.URL.Host)
		}
		_, _ = io.WriteString(w, "custom proxy reached")
	}))
	t.Cleanup(proxy.Close)
	t.Cleanup(http.DefaultClient.CloseIdleConnections)
	settings.ProxyURL, settings.NoProxy = "  "+proxy.URL+"  ", "  internal.example,192.168.0.0/16  "
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Store.Settings()
	if err != nil || saved.ProxyURL != proxy.URL || saved.NoProxy != strings.TrimSpace(settings.NoProxy) {
		t.Fatal("custom proxy settings did not persist or trim whitespace", saved, err)
	}
	response, err := http.Get("http://spool-custom-proxy.invalid/check")
	if err != nil {
		t.Fatal("saved proxy did not apply immediately", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "custom proxy reached" {
		t.Fatal(string(body), err)
	}
	for _, target := range []string{"https://internal.example", "https://192.168.1.2", "http://127.0.0.1:8787"} {
		u, _ := url.Parse(target)
		if proxy, err := netproxy.Resolve(u); err != nil || proxy != nil {
			t.Fatal("proxy bypass did not apply", proxy, err)
		}
	}
	settings.ProxyURL = "http://user:private-password@proxy.invalid/unsupported-path"
	if err := service.SaveSettings(settings); err == nil || strings.Contains(err.Error(), "private-password") {
		t.Fatal("invalid proxy accepted or error disclosed credentials", err)
	}
	if current, _ := service.Store.Settings(); current != saved {
		t.Fatal("invalid proxy changed persisted settings")
	}
	if status := service.Status(); status.RestartRequired || !status.Running || service.app != app || service.runner != runner {
		t.Fatal("proxy save interrupted the HTTP service", status)
	}
	settings = saved
	settings.ProxyEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://spool-custom-proxy.invalid")
	if proxy, err := netproxy.Resolve(target); err != nil || proxy != nil {
		t.Fatal("master switch did not force direct connections", proxy, err)
	}
	disabled, err := service.Store.Settings()
	if err != nil || disabled.ProxyEnabled || disabled.ProxyURL != saved.ProxyURL || disabled.NoProxy != saved.NoProxy {
		t.Fatal("disabling proxy discarded custom values", disabled, err)
	}
	settings.ProxyEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if proxy, err := netproxy.Resolve(target); err != nil || proxy == nil || proxy.String() != saved.ProxyURL {
		t.Fatal("re-enabling proxy did not restore the saved URL", proxy, err)
	}
	settings.ProxyURL, settings.NoProxy = "", ""
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal("custom proxy settings could not be cleared", err)
	}
}

func TestProxyChangesOnlyMarkActiveTailnetForExplicitRestart(t *testing.T) {
	service, settings := settingsService(t)
	connection := newTestTailnetConnection(t)
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return connection, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	app := service.app
	for _, next := range []Settings{
		func() Settings { next := settings; next.ProxyEnabled = false; return next }(),
		func() Settings { next := settings; next.ProxyURL = "http://127.0.0.1:8889"; return next }(),
		func() Settings { next := settings; next.NoProxy = "internal.example"; return next }(),
	} {
		if err := service.SaveSettings(next); err != nil {
			t.Fatal(err)
		}
		if status := service.Status(); !status.RestartRequired || status.TSNet.URL == "" || service.app != app {
			t.Fatal("proxy edit did not mark tailnet for restart or interrupted it", status)
		}
		if err := service.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if service.Status().RestartRequired {
			t.Fatal("reverting proxy changes did not clear the restart notice")
		}
	}
	settings.ProxyEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	connection = newTestTailnetConnection(t)
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	settings.ProxyURL, settings.NoProxy = "http://127.0.0.1:9999", "internal.example"
	settings.UseSystemProxy = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if service.Status().RestartRequired {
		t.Fatal("editing inactive proxy settings requested a tailnet restart")
	}
}

func TestProxySettingsMigrateAndPersistModes(t *testing.T) {
	store := testStore(t)
	// Older settings have no master switch; preserve their environment behavior.
	if _, err := store.db.Exec(`INSERT INTO settings VALUES('server','{"use_system_proxy":true}')`); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Settings()
	if err != nil || !settings.ProxyEnabled || !settings.UseSystemProxy {
		t.Fatal("older settings lost proxy behavior", settings, err)
	}
	settings.ProxyEnabled = false
	settings.ProxyURL, settings.NoProxy = "http://user:secret@proxy.example:8889", "internal.example"
	if err := store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Settings()
	if err != nil || saved != settings {
		t.Fatal("proxy settings did not round trip", err)
	}
	if options := saved.proxyOptions(); options != (netproxy.Options{}) {
		t.Fatal("disabled settings leaked an active proxy option")
	}
	saved.ProxyEnabled = true
	if options := saved.proxyOptions(); options != (netproxy.Options{Enabled: true, UseSystemProxy: true}) {
		t.Fatal("system mode did not ignore the saved custom fields")
	}
}
