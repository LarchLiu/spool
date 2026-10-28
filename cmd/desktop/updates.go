package main

import (
	"context"
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"spool/internal/appupdate"
)

// Initialize before services start using HTTP; MyGo has a process-wide client.
var signedUpdatesReady, signedUpdatesError = appupdate.RequireSignedManifests()

func (p *panel) initUpdates() {
	p.updates = appupdate.New(appupdate.Options{
		Enabled: signedUpdatesReady && signedUpdatesError == nil && mygo.Updater.Enabled(),
		Log:     p.updateLog,
		Check: func(ctx context.Context) (*appupdate.Release, error) {
			up, err := mygo.Updater.Check(ctx)
			if err != nil || up == nil {
				return nil, err
			}
			return &appupdate.Release{Version: up.Version, Notes: up.Notes, Install: up.Install}, nil
		},
		Acquire: p.service.AcquireUpdate,
		Preferences: func() (appupdate.Preferences, error) {
			p, err := p.service.Store.UpdatePreferences()
			return appupdate.Preferences{AutomaticChecks: p.AutomaticChecks, AutomaticInstalls: p.AutomaticInstalls}, err
		},
	})
}

func (p *panel) updateLog(phase, version string, err error) {
	log.Printf("update: %s %s %s", phase, version, appupdate.SafeError(err))
	if p.updateDiagnostics != nil {
		if e := p.updateDiagnostics.Record(phase, version, err); e != nil {
			log.Printf("write update log: %v", e)
		}
	}
}

func (p *panel) showUpdates() {
	p.selectTab("Updates")
	p.showMainWindow()
}

func (p *panel) updatesView(c *ui.Context) {
	state := p.updates.State()
	ui.Text(c, p.tr("Current version: {version}", "version", mygo.App.Version())).Bold()
	ui.Text(c, p.tr("Updates are signed and downloaded from GitHub Releases. Running chats always block installation and restart.")).Wrap().FontSize(12)
	if p.updateDiagnostics != nil {
		ui.Text(c, p.tr("Update log: {path}", "path", p.updateDiagnostics.Path)).Wrap().FontSize(11)
		if ui.Button(c, p.tr("Show update log")).Clicked() {
			mygo.Shell.ShowItemInFolder(p.updateDiagnostics.Path)
		}
	}
	if !state.Enabled {
		ui.Text(c, p.tr("Updates are unavailable in development builds or read-only installations. Install an official release in a writable location.")).Wrap()
		return
	}
	if !state.LastCheck.IsZero() {
		ui.Text(c, p.tr("Last checked: {time}", "time", state.LastCheck.Local().Format("2006-01-02 15:04"))).FontSize(12)
	}
	if state.Error != "" {
		phase := map[string]string{"check": "Update check failed.", "install": "Update installation failed.", "restart": "Update restart failed."}[state.ErrorPhase]
		if phase != "" {
			ui.Text(c, p.tr(phase)).Bold()
		}
		ui.Text(c, p.tr(state.Error)).Wrap()
		ui.Text(c, p.tr("Check your network and installation permissions, then retry. Signature failures are never bypassed.")).Wrap().FontSize(12)
	}
	switch {
	case state.Checking:
		ui.Text(c, p.tr("Checking for updates...")).Wrap()
	case state.Installing:
		ui.Text(c, p.tr("Installing update. New chat responses are temporarily paused.")).Wrap()
		if state.Total > 0 {
			ui.Text(c, fmt.Sprintf("%.0f%% (%d / %d MB)", float64(state.Downloaded)*100/float64(state.Total), state.Downloaded/(1024*1024), state.Total/(1024*1024))).FontSize(12)
		}
	case state.Installed:
		ui.Text(c, p.tr("Update installed. Restart when all chats have finished, or use it on the next launch.")).Wrap()
	case state.Version != "":
		ui.Text(c, p.tr("Update {version} is available.", "version", state.Version)).Bold()
	case !state.LastCheck.IsZero():
		ui.Text(c, p.tr("You are up to date.")).Wrap()
	}
	busy := state.Checking || state.Installing
	ui.Row(c).Gap(10).Children(func() {
		if ui.Button(c, p.tr("Check for updates")).Disabled(busy || state.Installed).Clicked() {
			go p.updates.Check()
		}
		if state.Version != "" && !state.Installed {
			if ui.PrimaryButton(c, p.tr("Install update")).Disabled(busy).Clicked() {
				go p.updates.Install()
			}
		}
		if state.Installed {
			if ui.PrimaryButton(c, p.tr("Restart to update")).Disabled(p.updateRelease != nil).Clicked() {
				release, err := p.updates.PrepareRestart()
				if err == nil {
					if p.updateDiagnostics != nil {
						if e := p.updateDiagnostics.RequestRestart(state.Version); e != nil {
							release()
							p.updates.RestartFailed(e)
							return
						}
					}
					p.updateRelease = release
					mygo.App.Relaunch()
				}
			}
		}
	})
	previous := p.updatePreferences
	ui.Checkbox(c, &p.updatePreferences.AutomaticChecks, p.tr("Automatically check for updates"))
	ui.Checkbox(c, &p.updatePreferences.AutomaticInstalls, p.tr("Automatically install updates when no chats are running"))
	if previous != p.updatePreferences {
		if err := p.service.Store.SaveUpdatePreferences(p.updatePreferences); err != nil {
			p.updatePreferences = previous
			p.message = err.Error()
		}
	}
	ui.Text(c, p.tr("Automatic updates never restart the app. Busy updates are retried after chats finish.")).Wrap().FontSize(12)
	if state.Notes != "" {
		ui.Text(c, p.tr("Release notes")).Bold()
		ui.Text(c, state.Notes).Wrap().FontSize(13)
	}
}
