package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"spool/internal/appupdate"
	"spool/internal/workspace"
)

func updateTestPanel(t *testing.T) (*panel, *int) {
	t.Helper()
	store, err := workspace.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	relaunches := 0
	p := &panel{service: &workspace.Service{Store: store}, updateRelaunch: func() { relaunches++ }}
	p.updates = appupdate.New(appupdate.Options{
		Enabled: true,
		Check: func(context.Context) (*appupdate.Release, error) {
			return &appupdate.Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error { return nil }}, nil
		},
		Acquire: p.service.AcquireUpdate,
	})
	t.Cleanup(p.updates.Close)
	t.Cleanup(func() {
		if p.updateRelease != nil {
			p.updateRelease()
		}
	})
	p.updateDiagnostics, err = appupdate.OpenDiagnostics(store.Root(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	p.updates.Check()
	return p, &relaunches
}

func saveAutomaticUpdates(t *testing.T, p *panel, enabled bool) {
	t.Helper()
	if err := p.service.Store.SaveUpdatePreferences(workspace.UpdatePreferences{AutomaticInstalls: enabled}); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticRestartUsesSavedSwitchAndHoldsLeaseUntilShutdown(t *testing.T) {
	p, relaunches := updateTestPanel(t)
	p.updates.Install()
	p.updatePreferences.AutomaticInstalls = true
	p.autoRestartForUpdate()
	if *relaunches != 0 {
		t.Fatal("default-off saved preference was replaced by a stale UI copy")
	}
	saveAutomaticUpdates(t, p, true)
	p.updatePreferences.AutomaticInstalls = false
	p.autoRestartForUpdate()
	if *relaunches != 1 || p.updateRelease == nil {
		t.Fatal("saved automatic-update switch did not restart the app")
	}
	if release, err := p.service.AcquireUpdate(); !errors.Is(err, workspace.ErrUpdateInProgress) {
		if release != nil {
			release()
		}
		t.Fatalf("restart did not hold the exclusive lease: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.service.Store.Root(), "update-pending.json")); err != nil {
		t.Fatal("automatic restart did not persist its diagnostic journal:", err)
	}
	p.autoRestartForUpdate()
	if *relaunches != 1 {
		t.Fatal("automatic restart was requested twice")
	}
}

func TestAutomaticRestartWaitsForInstallationAndSkipsQuit(t *testing.T) {
	p, relaunches := updateTestPanel(t)
	saveAutomaticUpdates(t, p, true)
	p.autoRestartForUpdate()
	if *relaunches != 0 {
		t.Fatal("app restarted before an update was installed")
	}
	p.updates.Install()
	p.quitting = true
	p.autoRestartForUpdate()
	if *relaunches != 0 {
		t.Fatal("app requested another restart during shutdown")
	}
}

func TestAutomaticRestartReacquiresServiceLeaseAfterInstallation(t *testing.T) {
	p, relaunches := updateTestPanel(t)
	saveAutomaticUpdates(t, p, true)
	p.updates.Install()
	release, err := p.service.AcquireUpdate()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	p.autoRestartForUpdate()
	if *relaunches != 0 || p.updateRelease != nil || p.updates.State().ErrorPhase != "restart" {
		t.Fatal("automatic restart bypassed a busy service")
	}
	release()
	p.autoRestartForUpdate()
	if *relaunches != 1 || p.updates.State().Error != "" {
		t.Fatal("automatic restart did not recover after the service became idle")
	}
}

func TestQueuedAutomaticRestartHonorsDisabledOrUnreadablePreferences(t *testing.T) {
	p, relaunches := updateTestPanel(t)
	p.updates.Install()
	saveAutomaticUpdates(t, p, true)
	saveAutomaticUpdates(t, p, false)
	p.autoRestartForUpdate()
	if *relaunches != 0 {
		t.Fatal("queued automatic restart ignored a disabled switch")
	}
	saveAutomaticUpdates(t, p, true)
	if err := p.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	p.autoRestartForUpdate()
	if *relaunches != 0 {
		t.Fatal("automatic restart continued with unreadable preferences")
	}
}

func TestAutomaticRestartFailureReleasesLeaseAndAllowsManualRetry(t *testing.T) {
	p, relaunches := updateTestPanel(t)
	saveAutomaticUpdates(t, p, true)
	p.updates.Install()
	journal := filepath.Join(p.service.Store.Root(), "update-pending.json")
	if err := os.Mkdir(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	p.autoRestartForUpdate()
	if *relaunches != 0 || p.updateRelease != nil || p.updates.State().ErrorPhase != "restart" {
		t.Fatal("failed journal write restarted the app or retained its lease")
	}
	release, err := p.service.AcquireUpdate()
	if err != nil {
		t.Fatal("failed automatic restart blocked subsequent chats:", err)
	}
	release()
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	saveAutomaticUpdates(t, p, false)
	if err := p.restartForUpdate(); err != nil || *relaunches != 1 {
		t.Fatal("manual retry no longer works with the automatic switch off:", err)
	}
}
