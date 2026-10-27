package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"spool/internal/appupdate"
)

func (p *panel) initUpdates() {
	p.updates = appupdate.New(appupdate.Options{
		Enabled: mygo.Updater.Enabled(),
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

func (p *panel) showUpdates() {
	p.tab = "Updates"
	p.message = ""
	p.window.Show()
	p.window.Focus()
}

func (p *panel) updatesView(c *ui.Context) {
	state := p.updates.State()
	ui.Text(c, p.tr("Current version: {version}", "version", mygo.App.Version())).Bold()
	ui.Text(c, p.tr("Updates are signed and downloaded from GitHub Releases. Running chats always block installation and restart.")).Wrap().FontSize(12)
	if !state.Enabled {
		ui.Text(c, p.tr("Updates are unavailable in development builds or read-only installations. Install an official release in a writable location.")).Wrap()
		return
	}
	if !state.LastCheck.IsZero() {
		ui.Text(c, p.tr("Last checked: {time}", "time", state.LastCheck.Local().Format("2006-01-02 15:04"))).FontSize(12)
	}
	if state.Error != "" {
		ui.Text(c, p.tr(state.Error)).Wrap()
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
