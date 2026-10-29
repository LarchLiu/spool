package workspace

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
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
		{"idle timeout", func(s *Settings) { s.IdleMinutes++ }, true},
		{"cache", func(s *Settings) { s.HistoryCacheMiB++ }, false},
		{"auto-start", func(s *Settings) { s.AutoStart = false }, false},
		{"unchanged", func(s *Settings) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := settings
			tc.change(&next)
			if err := service.Store.SaveSettings(next); err != nil {
				t.Fatal(err)
			}
			if status := service.Status(); !status.Running || status.RestartRequired != tc.restart {
				t.Fatalf("incorrect pending restart status: %+v", status)
			}
			if service.app != app || service.runner != runner || runner.IdleTimeout != time.Duration(settings.IdleMinutes)*time.Minute {
				t.Fatal("saving settings restarted or mutated the running service")
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
			if err := service.Store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if service.Status().RestartRequired {
				t.Fatal("reverting to the active settings did not clear the notice")
			}
		})
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
