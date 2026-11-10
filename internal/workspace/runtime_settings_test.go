package workspace

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"spool/internal/tailnet"
)

func TestRestartDuringUpdateDoesNotStopTheRunningService(t *testing.T) {
	service, _ := settingsService(t)
	app := service.app
	release, err := service.AcquireUpdate()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := service.Restart(); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatal("restart bypassed the update gate", err)
	}
	if !service.Status().Running || service.app != app {
		t.Fatal("blocked restart stopped the running service")
	}
}

func settingsService(t *testing.T) (*Service, Settings) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SPOOL_CODEX_BIN", filepath.Join(root, "no-codex"))
	t.Setenv("SPOOL_CLAUDE_BIN", filepath.Join(root, "no-claude"))
	service, err := OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	settings := DefaultSettings()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	return service, settings
}

func TestSavingServiceSettingsOnlyMarksChangesThatNeedRestart(t *testing.T) {
	service, settings := settingsService(t)
	app, runner := service.app, service.runner
	address, _ := service.LocalWebURL()
	for _, tc := range []struct {
		name    string
		change  func(*Settings)
		restart bool
	}{
		{"host", func(s *Settings) { s.Host = "0.0.0.0" }, true},
		{"port", func(s *Settings) { s.Port = s.Port%65535 + 1 }, true},
		{"public origin", func(s *Settings) { s.PublicURL = "https://chat.example.test" }, true},
		{"trusted Web proxies", func(s *Settings) { s.WebTrustedProxies = "192.0.2.10/32" }, true},
		{"idle timeout", func(s *Settings) { s.IdleMinutes++ }, false},
		{"cache", func(s *Settings) { s.HistoryCacheMiB++ }, false},
		{"auto-start", func(s *Settings) { s.AutoStart = false }, false},
		{"enable tsnet", func(s *Settings) { s.TSNetEnabled = true }, true},
		{"inactive tsnet hostname", func(s *Settings) { s.TSNetHostname = "other-node" }, false},
		{"inactive tsnet port", func(s *Settings) { s.TSNetPort = 8443 }, false},
		{"unchanged", func(s *Settings) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := settings
			tc.change(&next)
			if err := service.SaveSettings(next); err != nil {
				t.Fatal(err)
			}
			if status := service.Status(); !status.Running || status.RestartRequired != tc.restart {
				t.Fatalf("incorrect pending restart status: %+v", status)
			}
			if service.app != app || service.runner != runner || runner.IdleTimeout != time.Duration(next.IdleMinutes)*time.Minute {
				t.Fatal("saving settings restarted the service or failed to apply the idle timeout")
			}
			select {
			case <-app.shutdown:
				t.Fatal("saving settings interrupted active tasks")
			default:
			}
			if current, err := service.LocalWebURL(); err != nil || current != address {
				t.Fatal("saving settings changed the running desktop endpoint", current, err)
			}
			service.Store.historyCache.mu.Lock()
			limit := service.Store.historyCache.maxBytes
			service.Store.historyCache.mu.Unlock()
			if limit != next.HistoryCacheMiB*1024*1024 {
				t.Fatal("cache change did not apply immediately", limit)
			}
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if service.Status().RestartRequired {
				t.Fatal("reverting to the active settings did not clear the notice")
			}
		})
	}
}

func TestSavingRuntimeSettingsAppliesImmediatelyWithoutRestart(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	connection := newTestTailnetConnection(t)
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return connection, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.Relay.State == "connected" })
	app, runner, server, web := service.app, service.runner, service.server, service.web
	node, relayEntry := service.tailnet, service.relay
	localURL, _ := service.LocalWebURL()
	app.turnMu.Lock()
	work, control := app.registerTurn(t.Context(), "runtime-save", "turn")
	app.turnMu.Unlock()
	defer app.releaseTurn("runtime-save", control)
	for _, minutes := range []int{30, 1, settings.IdleMinutes} {
		settings.IdleMinutes = minutes
		settings.HistoryCacheMiB++
		if err := service.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if service.runner.IdleTimeout != time.Duration(minutes)*time.Minute || service.app.activeSettings().IdleMinutes != minutes {
			t.Fatal("idle timeout did not apply when saved")
		}
		if status := service.Status(); status.RestartRequired || len(status.PendingChanges) != 0 {
			t.Fatal("runtime save requires another apply action", status)
		}
		if service.app != app || service.runner != runner || service.server != server || service.web != web || service.tailnet != node || service.relay != relayEntry || work.Err() != nil {
			t.Fatal("runtime save restarted a service or interrupted work")
		}
		if current, err := service.LocalWebURL(); err != nil || current != localURL {
			t.Fatal("runtime save replaced the desktop endpoint", current, err)
		}
		service.Store.historyCache.mu.Lock()
		limit := service.Store.historyCache.maxBytes
		service.Store.historyCache.mu.Unlock()
		if limit != settings.HistoryCacheMiB*1024*1024 {
			t.Fatal("cache size did not apply when saved", limit)
		}
	}
	requireServiceHealth(t, localURL)
	requireServiceHealth(t, "http://"+service.Status().Address)
	requireServiceHealth(t, "http://"+connection.Listener().Addr().String())
	invalid := settings
	invalid.IdleMinutes = 0
	if err := service.SaveSettings(invalid); err == nil {
		t.Fatal("invalid runtime setting was accepted")
	}
	saved, _ := service.Store.Settings()
	if saved.IdleMinutes != settings.IdleMinutes || runner.IdleTimeout != time.Duration(settings.IdleMinutes)*time.Minute {
		t.Fatal("failed save changed the active or saved idle timeout")
	}
}

func TestExplicitServiceRestartAppliesSavedSettingsAndClearsNotice(t *testing.T) {
	service, settings := settingsService(t)
	oldApp := service.app
	settings.IdleMinutes = 30
	settings.PublicURL = "https://chat.example.test"
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !service.Status().RestartRequired {
		t.Fatal("missing pending restart notice")
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); !status.Running || status.RestartRequired {
		t.Fatal("restart did not apply saved settings", status)
	}
	if service.app == oldApp || service.runner.IdleTimeout != 30*time.Minute || service.WebURL() != settings.PublicURL {
		t.Fatal("restart did not replace the runtime with saved settings")
	}
	select {
	case <-oldApp.shutdown:
	default:
		t.Fatal("explicit restart did not stop the old runtime")
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	settings.IdleMinutes++
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if service.Status().RestartRequired {
		t.Fatal("a stopped service must not require a restart")
	}
	if err := service.Start(); err != nil || service.Status().RestartRequired {
		t.Fatal("next start did not use saved settings", err)
	}
}

func TestFailedServiceRestartReportsBindErrorAndCanBeRetried(t *testing.T) {
	service, settings := settingsService(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	settings.Port = listener.Addr().(*net.TCPAddr).Port
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Restart(); err == nil {
		t.Fatal("restart hid the occupied port error")
	}
	if status := service.Status(); status.Running || status.Error == "" {
		t.Fatal("failed restart did not expose a stopped/error state", status)
	}
	listener.Close()
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	address, _ := service.LocalWebURL()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(address + "/healthz")
	if err != nil {
		t.Fatal("saved configuration could not be retried", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || service.Status().RestartRequired {
		t.Fatal("service did not recover on the saved port", response.Status)
	}
}
