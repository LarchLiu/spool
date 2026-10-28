package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"

	"spool"
	"spool/internal/appupdate"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

type panel struct {
	service            *workspace.Service
	window             *mygo.Window
	tray               *mygo.Tray
	logo               *ui.Bitmap
	preferences        workspace.UIPreferences
	appliedPreferences workspace.UIPreferences
	tab                string
	message            string
	busy               bool
	settings           workspace.Settings
	port, idle         string
	historyCache       string
	users              []workspace.User
	projects           []workspace.Project
	agents             []workspace.AgentInfo
	status             workspace.ServiceStatus
	user               workspace.UserInput
	project            workspace.Project
	issueInterval      string
	directoryPicker    directoryPicker
	members            map[string]bool
	agentPaths         map[chatmodel.Backend]string
	agentEnabled       map[chatmodel.Backend]bool
	updates            *appupdate.Controller
	updatePreferences  workspace.UpdatePreferences
	updateRelease      func()
	updateDiagnostics  *appupdate.Diagnostics
}

func main() {
	if err := runDesktop(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func runDesktop() (runErr error) {
	if signedUpdatesError != nil {
		log.Printf("Signed updates disabled: %v", signedUpdatesError)
	}
	root := flag.String("data-dir", workspace.DefaultDataDir(), "SQLite data directory")
	smoke := flag.String("release-smoke", "", "Probe the packaged desktop using fresh temporary data; write a JSON report")
	flag.Parse()
	if *smoke != "" {
		temporary, err := smokeData()
		if err != nil {
			return err
		}
		defer os.RemoveAll(temporary)
		defer func() {
			if data, e := os.ReadFile(filepath.Join(temporary, "update.log")); e == nil {
				_ = os.WriteFile(*smoke+".log", data, 0o600)
			}
		}()
		*root = temporary
	}
	mygo.App.SetName("Spool")
	mygo.App.SetLanguages("en", "zh-CN", "ja")
	if *smoke == "" && !mygo.App.RequestSingleInstanceLock() {
		log.Print("Spool is already running; focusing the existing desktop instance")
		return nil
	}
	if err := os.MkdirAll(*root, 0o700); err != nil {
		return err
	}
	diagnostics, err := appupdate.OpenDiagnostics(*root, mygo.App.Version())
	if err != nil {
		log.Printf("Update diagnostics unavailable: %v", err)
	}
	defer func() {
		if runErr != nil && diagnostics != nil {
			_ = diagnostics.Record("startup_failed", mygo.App.Version(), runErr)
		}
	}()
	service, err := workspace.OpenService(*root)
	if err != nil {
		return err
	}
	defer service.Close()
	p := &panel{service: service, tab: "Service", members: map[string]bool{}, agentPaths: map[chatmodel.Backend]string{}, agentEnabled: map[chatmodel.Backend]bool{}}
	p.updateDiagnostics = diagnostics
	p.logo, err = ui.DecodeBitmap(spool.AppIcon())
	if err != nil {
		return err
	}
	p.load()
	p.initUpdates()
	mygo.Theme.SetSource(mygo.ThemeSource(p.preferences.Theme))
	if p.settings.AutoStart {
		if err = service.Start(); err != nil {
			p.message = err.Error()
		}
	}
	done := make(chan struct{})
	smokeResult := make(chan error, 1)
	var shutdownErr error
	mygo.App.OnQuit(func() {
		p.updateLog("shutdown_started", "", nil)
		close(done)
		p.updates.Close()
		// MyGo relaunches after OnQuit, before main's deferred cleanup.
		if err := service.Close(); err != nil {
			shutdownErr = err
			p.updateLog("shutdown_failed", "", err)
		} else {
			p.updateLog("shutdown_completed", "", nil)
		}
		if p.updateRelease != nil {
			p.updateRelease()
		}
	})
	var quitting bool
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if p.updates.State().Installing {
			e.PreventDefault()
			p.showUpdates()
			p.message = "Wait for the update to finish before quitting."
			return
		}
		quitting = true
	})
	mygo.App.WhenReady(func() {
		p.applyPresentation()
		mygo.App.OnLocaleChanged(func(string) { p.updateNativeLabels() })
		p.window = mygo.NewWindow(mygo.WindowOptions{Title: "Spool · " + p.tr("Control Center"), Width: 960, Height: 760, MinWidth: 740, MinHeight: 580, Content: ui.View(p.view)})
		p.window.OnClose(func(e *mygo.CloseEvent) {
			if !quitting {
				e.PreventDefault()
				p.window.Hide()
			}
		})
		mygo.App.OnSecondInstance(func([]string, string) { p.window.Show(); p.window.Focus() })
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				p.window.Show()
				p.window.Focus()
			}
		})
		var e error
		p.tray, e = mygo.NewTray(mygo.TrayOptions{Icon: spool.TrayIcon(), IconIsTemplate: false, ToolTip: "Spool · " + p.tr("Web service"), Menu: p.trayMenu()})
		if e != nil {
			p.message = p.tr("Tray unavailable") + ": " + e.Error()
		}
		go func() {
			p.window.Update(func() {
				if diagnostics != nil {
					if e := diagnostics.Ready(); e != nil {
						p.message = e.Error()
						log.Print(e)
					}
				}
			})
		}()
		if *smoke == "" {
			p.updates.Start()
		} else {
			p.window.Show()
			go p.smoke(*smoke, smokeResult)
		}

		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					status := service.Status()
					agents, _ := service.Store.Agents()
					projects, _ := service.Store.Projects()
					p.window.Update(func() { p.status = status; p.agents = agents; p.projects = projects })
				}
			}

		}()
	})
	if err = mygo.App.Run(); err != nil {
		return err
	}
	if *smoke != "" {
		if shutdownErr != nil {
			return shutdownErr
		}
		select {
		case err = <-smokeResult:
			return err
		default:
			return fmt.Errorf("desktop exited before the release probe completed")
		}
	}
	return nil
}
func (p *panel) load() {
	p.preferences, _ = p.service.Store.UIPreferences("")
	p.settings, _ = p.service.Store.Settings()
	p.port = strconv.Itoa(p.settings.Port)
	p.idle = strconv.Itoa(p.settings.IdleMinutes)
	p.historyCache = strconv.Itoa(p.settings.HistoryCacheMiB)
	p.users, _ = p.service.Store.Users()
	p.projects, _ = p.service.Store.Projects()
	p.agents, _ = p.service.Store.Agents()
	p.status = p.service.Status()
	p.updatePreferences, _ = p.service.Store.UpdatePreferences()
	for _, a := range p.agents {
		p.agentPaths[a.Backend] = a.Executable
		p.agentEnabled[a.Backend] = a.Enabled
	}
}
func (p *panel) action(fn func() error) {
	if p.busy {
		return
	}
	p.busy = true
	p.message = "Working…"
	go func() {
		err := fn()
		p.window.Update(func() {
			p.busy = false
			if err != nil {
				p.message = err.Error()
			} else {
				p.message = "Saved"
				p.load()
				p.applyPresentation()
			}
		})
	}()
}
func (p *panel) view(c *ui.Context) {
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(176).FillHeight().Padding(22).Gap(10).Children(func() {
			if p.logo != nil {
				ui.Image(c, p.logo).Size(36, 36)
			}
			ui.Text(c, "Spool").Bold().FontSize(16)
			ui.Text(c, p.tr("Control Center")).FontSize(12)
			for _, tab := range []string{"Service", "People", "Projects", "Agents", "Online", "Appearance", "Updates"} {
				c.Key("tab-" + tab)
				if ui.Button(c, p.tr(tab)).FillWidth().Clicked() {
					p.tab = tab
					p.message = ""
				}
			}
			ui.Text(c, p.tr("Desktop administration only")).FontSize(11)
		})
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).FillWidth().Padding(28).Gap(16).Children(func() {
				ui.Text(c, p.tr(p.tab)).Bold().FontSize(27)
				if p.message != "" {
					ui.Text(c, p.tr(p.message)).Wrap().FontSize(12)
				}
				if state := p.updates.State(); state.Version != "" && p.tab != "Updates" {
					ui.Text(c, p.tr("Update {version} is available.", "version", state.Version)).Wrap().FontSize(12)
					if state.Error != "" {
						ui.Text(c, p.tr(state.Error)).Wrap().FontSize(12)
					}
				}
				switch p.tab {
				case "Service":
					p.serviceView(c)
				case "People":
					p.peopleView(c)
				case "Projects":
					p.projectView(c)
				case "Agents":
					p.agentView(c)
				case "Online":
					p.onlineView(c)
				case "Appearance":
					p.appearanceView(c)
				case "Updates":
					p.updatesView(c)
				}
			})
		})
	})
}
func (p *panel) field(c *ui.Context, label string, value *string) {
	label = p.tr(label)
	ui.Column(c).FillWidth().Gap(5).Children(func() { ui.Text(c, label).FontSize(12); ui.TextInput(c, value).FillWidth().Label(label) })
}
func (p *panel) serviceView(c *ui.Context) {
	status := "Stopped"
	if p.status.Running {
		status = "Running"
	}
	ui.Text(c, p.tr(status)+" · "+p.status.Address).Bold()
	if p.status.Error != "" {
		ui.Text(c, p.status.Error).Wrap()
	}
	ui.Row(c).Gap(10).Children(func() {
		if ui.PrimaryButton(c, p.tr("Start service")).Disabled(p.busy || p.status.Running).Clicked() {
			p.action(p.service.Start)
		}
		if ui.Button(c, p.tr("Stop service")).Disabled(p.busy || !p.status.Running).Clicked() {
			p.action(p.service.Stop)
		}
		if ui.Button(c, p.tr("Open Web Chat")).Clicked() {
			_ = mygo.Shell.OpenExternal(p.service.WebURL())
		}
	})
	p.field(c, "Listen IP (127.0.0.1 / 0.0.0.0)", &p.settings.Host)
	p.field(c, "Web port", &p.port)
	p.field(c, "Public origin (HTTPS reverse proxy for remote PWA)", &p.settings.PublicURL)
	p.field(c, "Release completed agents after idle minutes", &p.idle)
	p.field(c, "History cache size (MiB)", &p.historyCache)
	ui.Text(c, p.tr("Shared by all chats. Applies immediately after saving. A larger session is kept whole; history is never truncated.")).Wrap().FontSize(12)
	ui.Checkbox(c, &p.settings.AutoStart, p.tr("Start Web service on application launch"))
	if ui.PrimaryButton(c, p.tr("Save settings")).Disabled(p.busy).Clicked() {
		settings := p.settings
		port, e := strconv.Atoi(p.port)
		idle, e2 := strconv.Atoi(p.idle)
		cache, e3 := strconv.Atoi(p.historyCache)
		if e != nil || e2 != nil || e3 != nil {
			p.message = "Port, idle minutes and history cache size must be integers"
		} else {
			settings.Port = port
			settings.IdleMinutes = idle
			settings.HistoryCacheMiB = cache
			p.action(func() error { return p.service.Store.SaveSettings(settings) })
		}
	}
	ui.Text(c, p.tr("Listen address and idle settings apply after Stop → Start. Stopping interrupts active tasks. Closing this window keeps the server in the tray; Quit stops it.")).Wrap().FontSize(12)
	ui.Text(c, "SQLite · "+p.service.Store.Root()).Wrap().FontSize(12)
	ui.Text(c, p.tr("Users can execute agent tools in granted projects. Only grant access to trusted people. HTTPS is required for remote PWA installation.")).Wrap().FontSize(12)
}
func (p *panel) peopleView(c *ui.Context) {
	ui.Text(c, p.tr("Accounts are created here. Web clients cannot register or administer users.")).Wrap().FontSize(12)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, p.tr("New person")).Clicked() {
			p.user = workspace.UserInput{}
		}
		for _, u := range p.users {
			u := u
			c.Key(u.ID)
			label := u.Nickname
			if u.Disabled {
				label += " (" + p.tr("Disabled") + ")"
			}
			if ui.Button(c, label).Clicked() {
				p.user = workspace.UserInput{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Disabled: u.Disabled}
			}
		}
	})
	p.field(c, "Username", &p.user.Username)
	p.field(c, "Display name", &p.user.Nickname)
	ui.Text(c, p.tr("Password (12–72 bytes; blank keeps existing password)")).FontSize(12)
	ui.TextInput(c, &p.user.Password).Password().FillWidth().Label(p.tr("Password"))
	ui.Checkbox(c, &p.user.Disabled, p.tr("Disable account"))
	if ui.PrimaryButton(c, p.tr("Save person")).Disabled(p.busy).Clicked() {
		input := p.user
		p.user.Password = ""
		p.action(func() error { _, err := p.service.Store.SaveUser(input); return err })
	}
	ui.Text(c, p.tr("Passwords are bcrypt-hashed in SQLite. Editing an account revokes its Web logins. Disabling keeps its chat history.")).Wrap().FontSize(12)
}
func (p *panel) projectView(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, p.tr("New project")).Disabled(p.directoryPicker.pending).Clicked() {
			p.project = workspace.Project{Issues: workspace.IssuesAutomation{IntervalMinutes: 60, Backend: chatmodel.BackendCodex}}
			p.issueInterval = "60"
			p.members = map[string]bool{}
		}
		for _, project := range p.projects {
			project := project
			c.Key(project.ID)
			if ui.Button(c, project.Name).Disabled(p.directoryPicker.pending).Clicked() {
				p.project = project
				p.issueInterval = strconv.Itoa(project.Issues.IntervalMinutes)
				p.members = map[string]bool{}
				for _, id := range project.Members {
					p.members[id] = true
				}
			}
		}
	})
	p.field(c, "Project name", &p.project.Name)
	p.field(c, "Local directory / agent working directory", &p.project.Path)
	if ui.Button(c, p.tr("Choose directory…")).Disabled(p.busy || p.directoryPicker.pending).Clicked() {
		p.directoryPicker.open(mygo.OpenDialogOptions{
			Parent: p.window, Title: p.tr("Project directory"),
			Directory: true, DefaultPath: p.project.Path,
		}, mygo.Dialog.Open, p.window.Update, p.applyProjectDirectory)
	}
	ui.Checkbox(c, &p.project.Archived, p.tr("Archive project (deny new access, keep history)"))
	ui.Text(c, p.tr("People allowed to use this project")).Bold()
	for _, u := range p.users {
		if u.Disabled {
			continue
		}
		c.Key("member-" + u.ID)
		checked := p.members[u.ID]
		ui.Checkbox(c, &checked, u.Nickname+" · "+u.Username)
		p.members[u.ID] = checked
	}
	ui.Checkbox(c, &p.project.SharedChats, p.tr("Share chats with all project members"))
	p.projectIssuesView(c)
	if ui.PrimaryButton(c, p.tr("Save project")).Disabled(p.busy || p.directoryPicker.pending).Clicked() {
		project := p.project
		if project.Issues.Enabled {
			interval, err := strconv.Atoi(p.issueInterval)
			if err != nil || interval < 1 || interval > 10080 {
				p.message = p.tr("Issue interval must be between 1 and 10080 minutes")
				return
			}
			project.Issues.IntervalMinutes = interval
		}
		project.Members = nil
		for id, enabled := range p.members {
			if enabled {
				project.Members = append(project.Members, id)
			}
		}
		p.action(func() error { _, err := p.service.Store.SaveProject(project); return err })
	}
	ui.Text(c, p.tr("When enabled, project members can view one another's chats; only chat owners can send messages or control runs.")).Wrap().FontSize(12)
	ui.Text(c, p.tr("Members share the actual directory and its filesystem permissions. A project's directory cannot change after chats exist.")).Wrap().FontSize(12)
}
func (p *panel) agentView(c *ui.Context) {
	if ui.Button(c, p.tr("Refresh installation & models")).Disabled(p.busy).Clicked() {
		p.action(func() error { return p.service.Registry.Refresh(context.Background()) })
	}
	for _, a := range p.agents {
		a := a
		c.Key(a.Backend)
		ui.Column(c).FillWidth().Gap(8).Padding(12).Children(func() {
			state := "Not installed"
			if a.Installed {
				state = "Installed"
			}
			ui.Text(c, a.Backend.Label()+" · "+p.tr(state)).Bold()
			enabled := p.agentEnabled[a.Backend]
			ui.Checkbox(c, &enabled, p.tr("Enabled"))
			p.agentEnabled[a.Backend] = enabled
			path := p.agentPaths[a.Backend]
			p.field(c, "CLI executable (blank = auto-detect)", &path)
			p.agentPaths[a.Backend] = path
			if ui.Button(c, p.tr("Save {agent}", "agent", string(a.Backend))).Disabled(p.busy).Clicked() {
				p.action(func() error { return p.service.SaveAgent(a.Backend, enabled, path) })
			}
			ui.Text(c, a.Version).FontSize(12)
			ui.Text(c, a.ResolvedPath).Wrap().FontSize(11)
			if a.Error != "" {
				ui.Text(c, a.Error).Wrap().FontSize(12)
			}
			ui.Text(c, p.tr("{count} models · checked {time}", "count", strconv.Itoa(len(a.Catalog.Models)), "time", time.UnixMilli(a.CheckedAt).Format("15:04:05"))).FontSize(12)
			for _, m := range a.Catalog.Models {
				levels := []string{}
				for _, e := range m.Efforts {
					levels = append(levels, e.Effort)
				}
				ui.Text(c, m.DisplayName+" · "+strings.Join(levels, " / ")).Wrap().FontSize(11)
			}
		})
	}
	ui.Text(c, p.tr("Discovery runs at launch and every 5 minutes, without submitting a model prompt. Failed discovery retains the last successful catalog.")).Wrap().FontSize(12)
}
func (p *panel) onlineView(c *ui.Context) {
	ui.Text(c, p.tr("{count} people connected", "count", strconv.Itoa(len(p.status.Online)))).Bold()
	for _, u := range p.status.Online {
		ui.Text(c, u.Nickname+" · "+p.tr(map[string]string{"online": "Online", "away": "Away"}[u.State])+" · "+p.tr("{count} connections", "count", strconv.Itoa(u.Devices)))
	}
	ui.Text(c, p.tr("Heartbeat timeout: 90 seconds. A backgrounded mobile PWA may appear away or offline; its agent task continues.")).Wrap().FontSize(12)
}
