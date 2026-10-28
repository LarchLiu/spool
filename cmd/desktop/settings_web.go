package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"spool"
	"spool/internal/appupdate"
	"spool/internal/issues"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

const settingsPageURL = "mygo://localhost/desktop.html"

var settingsSection = mygo.NewEvent[string]("spool:settings-section")

func desktopVersion() string {
	if version := mygo.App.Version(); version != "" {
		return strings.TrimPrefix(version, "v")
	}
	return spool.Version()
}

type desktopSettings struct {
	panel *panel
	mu    sync.Mutex
}

type settingsProject struct {
	workspace.Project
	Path      string                     `json:"path"`
	Members   []string                   `json:"members"`
	Issues    workspace.IssuesAutomation `json:"issues"`
	NextRun   int64                      `json:"next_run"`
	LastError string                     `json:"last_error"`
	LastChat  string                     `json:"last_chat"`
}

type settingsAgent struct {
	workspace.AgentInfo
	Executable   string `json:"executable"`
	ResolvedPath string `json:"resolved_path"`
}

type settingsPerson struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Password string `json:"password"`
	Disabled bool   `json:"disabled"`
}

type settingsSnapshot struct {
	Section           string                      `json:"section"`
	Message           string                      `json:"message"`
	Settings          workspace.Settings          `json:"settings"`
	Preferences       workspace.UIPreferences     `json:"preferences"`
	UpdatePreferences workspace.UpdatePreferences `json:"update_preferences"`
	Status            workspace.ServiceStatus     `json:"status"`
	Users             []workspace.User            `json:"users"`
	Projects          []settingsProject           `json:"projects"`
	Agents            []settingsAgent             `json:"agents"`
	Updates           appupdate.State             `json:"updates"`
	Version           string                      `json:"version"`
	Database          string                      `json:"database"`
	ApplicationLog    string                      `json:"application_log"`
	CrashLog          string                      `json:"crash_log"`
	UpdateLog         string                      `json:"update_log"`
	GitHubPath        string                      `json:"github_path"`
	GitHubState       string                      `json:"github_state"`
	GitHubMessage     string                      `json:"github_message"`
	GitHubLogin       string                      `json:"github_login"`
	SavedID           string                      `json:"saved_id,omitempty"`
	Repository        string                      `json:"repository,omitempty"`
}

type settingsCommand struct {
	Kind              string                      `json:"kind"`
	Section           string                      `json:"section"`
	Settings          workspace.Settings          `json:"settings"`
	Preferences       workspace.UIPreferences     `json:"preferences"`
	UpdatePreferences workspace.UpdatePreferences `json:"update_preferences"`
	Person            settingsPerson              `json:"person"`
	Project           settingsProject             `json:"project"`
	Backend           chatmodel.Backend           `json:"backend"`
	Enabled           bool                        `json:"enabled"`
	Path              string                      `json:"path"`
	Target            string                      `json:"target"`
}

func settingsDocumentAllowed(address string) bool {
	return address == settingsPageURL || address == "http://mygo.localhost/desktop.html"
}

func settingsAssets() (http.Handler, error) {
	assets, err := fs.Sub(spool.Frontend(), "frontend")
	if err != nil {
		return nil, err
	}
	files := mygo.FileServer(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}), nil
}

func (p *panel) loadSettingsView() error {
	if !mygo.WebViewAvailable() {
		return fmt.Errorf("Desktop chat requires a system WebView. Install WebView2 (Windows) or WebKitGTK (Linux), or use Open in Browser.")
	}
	view, err := p.window.NewWebView(chatWebViewOptions())
	if err != nil {
		return err
	}
	p.settingsView = view
	page := view.Page()
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if settingsDocumentAllowed(e.URL) {
			return
		}
		e.PreventDefault()
		if e.UserInitiated {
			p.openChatLink(e.URL)
		}
	})
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		p.openChatLink(req.URL)
		return nil
	})
	fail := func(err error) {
		p.window.Update(func() {
			p.settingsFailed = true
			p.message = err.Error()
		})
	}
	page.OnDidFailLoad(func(err *mygo.LoadError) { fail(err) })
	page.OnRenderProcessGone(func(reason string) { fail(fmt.Errorf("settings renderer stopped: %s", reason)) })
	page.OnDidFinishLoad(func() {
		if p.tab != "Chat" {
			view.Focus()
		}
	})
	return page.LoadURL(settingsPageURL)
}

// Bound calls run on worker goroutines. All panel state stays on the UI thread;
// the page identity check also denies local chat pages in development builds.
func settingsUI[T any](ctx context.Context, p *panel, fn func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	var zero T
	if p == nil || p.window == nil {
		return zero, errors.New("desktop settings are unavailable")
	}
	reply := make(chan result, 1)
	p.window.Update(func() {
		if err := ctx.Err(); err != nil {
			reply <- result{err: err}
			return
		}
		caller := mygo.CallerPage(ctx)
		if p.quitting || p.settingsView == nil || p.settingsFailed || caller == nil ||
			caller != p.settingsView.Page() || !settingsDocumentAllowed(caller.URL()) {
			reply <- result{err: errors.New("only the desktop settings page may manage this workspace")}
			return
		}
		value, err := fn()
		reply <- result{value, err}
	})
	select {
	case r := <-reply:
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (s *desktopSettings) Snapshot(ctx context.Context) (settingsSnapshot, error) {
	p := s.panel
	v, err := settingsUI(ctx, p, func() (settingsSnapshot, error) {
		v := settingsSnapshot{Section: p.tab, Message: p.message, Updates: p.updates.State(), Version: desktopVersion()}
		v.GitHubState, v.GitHubLogin = string(p.github.status.State), p.github.status.Login
		v.GitHubMessage = p.tr(p.github.message(), "user", p.github.status.Login)
		if p.runtimeDiagnostics != nil {
			v.ApplicationLog, v.CrashLog = p.runtimeDiagnostics.Path, p.runtimeDiagnostics.PreviousCrashPath
		}
		if p.updateDiagnostics != nil {
			v.UpdateLog = p.updateDiagnostics.Path
		}
		return v, nil
	})
	if err != nil {
		return v, err
	}
	store := p.service.Store
	if v.Settings, err = store.Settings(); err != nil {
		return v, err
	}
	if v.Preferences, err = store.UIPreferences(""); err != nil {
		return v, err
	}
	if v.UpdatePreferences, err = store.UpdatePreferences(); err != nil {
		return v, err
	}
	if v.Users, err = store.Users(); err != nil {
		return v, err
	}
	projects, err := store.Projects()
	if err != nil {
		return v, err
	}
	v.Projects = []settingsProject{}
	for _, project := range projects {
		v.Projects = append(v.Projects, settingsProject{project, project.Path, project.Members, project.Issues, project.Issues.NextRunAt, project.Issues.LastError, project.Issues.LastChatID})
	}
	agents, err := store.Agents()
	if err != nil {
		return v, err
	}
	v.Agents = []settingsAgent{}
	for _, agent := range agents {
		v.Agents = append(v.Agents, settingsAgent{agent, agent.Executable, agent.ResolvedPath})
	}
	v.GitHubPath, err = store.GitHubCLIPath()
	v.Status, v.Database = p.service.Status(), store.DatabasePath()
	return v, err
}

func settingsTabAllowed(tab string) bool {
	switch tab {
	case "Service", "People", "Projects", "Agents", "Online", "Appearance", "Updates":
		return true
	}
	return false
}

func (s *desktopSettings) Execute(ctx context.Context, command settingsCommand) (settingsSnapshot, error) {
	p := s.panel
	if _, err := settingsUI(ctx, p, func() (bool, error) { return true, nil }); err != nil {
		return settingsSnapshot{}, err
	}
	if !s.mu.TryLock() {
		return settingsSnapshot{}, errors.New("Working…")
	}
	defer s.mu.Unlock()
	store := p.service.Store
	var err error
	var savedID, repository string
	switch command.Kind {
	case "navigate", "open-chat", "open-browser", "show-file", "restart-update":
		_, err = settingsUI(ctx, p, func() (bool, error) {
			switch command.Kind {
			case "navigate":
				if !settingsTabAllowed(command.Section) {
					return false, errors.New("invalid settings section")
				}
				p.selectTab(command.Section)
			case "open-chat":
				p.openChat()
			case "open-browser":
				p.openBrowser()
			case "restart-update":
				return true, p.restartForUpdate()
			case "show-file":
				path := ""
				switch command.Target {
				case "database":
					path = store.DatabasePath()
				case "application":
					if p.runtimeDiagnostics != nil {
						path = p.runtimeDiagnostics.Path
					}
				case "crash":
					if p.runtimeDiagnostics != nil {
						path = p.runtimeDiagnostics.PreviousCrashPath
					}
				case "update":
					if p.updateDiagnostics != nil {
						path = p.updateDiagnostics.Path
					}
				}
				if path == "" {
					return false, errors.New("file is unavailable")
				}
				mygo.Shell.ShowItemInFolder(path)
			}
			return true, nil
		})
	case "start-service":
		err = p.service.Start()
	case "stop-service":
		err = p.service.Stop()
	case "save-settings":
		err = store.SaveSettings(command.Settings)
	case "save-appearance":
		err = store.SaveUIPreferences("", command.Preferences)
	case "save-updates":
		err = store.SaveUpdatePreferences(command.UpdatePreferences)
	case "save-person":
		person := command.Person
		var saved workspace.User
		saved, err = store.SaveUser(workspace.UserInput{ID: person.ID, Username: person.Username, Nickname: person.Nickname, Password: person.Password, Disabled: person.Disabled})
		savedID = saved.ID
	case "save-project":
		project := command.Project.Project
		project.Path, project.Members, project.Issues = command.Project.Path, command.Project.Members, command.Project.Issues
		// Preserve the original enablement gate: an existing schedule may be
		// edited offline, but a new one requires a successful GitHub CLI check.
		wasEnabled := false
		projects, e := store.Projects()
		if e != nil {
			err = e
			break
		}
		for _, old := range projects {
			if old.ID == project.ID {
				wasEnabled = old.Issues.Enabled
			}
		}
		if project.Issues.Enabled && !wasEnabled {
			status := issues.CheckCLI(ctx, store.RunIssueCommand)
			if status.State != issues.CLIReady {
				err = errors.New("Issue automation cannot be enabled until the GitHub CLI check succeeds.")
				break
			}
		}
		var saved workspace.Project
		saved, err = store.SaveProject(project)
		savedID = saved.ID
	case "save-agent":
		err = p.service.SaveAgent(command.Backend, command.Enabled, command.Path)
	case "refresh-agents":
		err = p.service.Registry.Refresh(ctx)
	case "save-github", "check-github":
		if command.Kind == "save-github" {
			err = store.SaveGitHubCLIPath(strings.TrimSpace(command.Path))
		}
		if err == nil {
			status := issues.CheckCLI(ctx, store.RunIssueCommand)
			_, err = settingsUI(ctx, p, func() (bool, error) { p.github.status, p.github.pending = status, false; return true, nil })
		}
	case "check-repository":
		repository, err = issues.DetectRepository(ctx, issues.Run, command.Path)
	case "check-updates":
		go p.updates.Check()
	case "install-update":
		go p.updates.Install()
	default:
		err = errors.New("unknown desktop settings command")
	}
	if err != nil {
		return settingsSnapshot{}, err
	}
	_, err = settingsUI(ctx, p, func() (bool, error) { p.load(); p.applyPresentation(); return true, nil })
	if err != nil {
		return settingsSnapshot{}, err
	}
	v, err := s.Snapshot(ctx)
	v.SavedID, v.Repository = savedID, repository
	return v, err
}

func (s *desktopSettings) ChoosePath(ctx context.Context, kind, current string) (string, error) {
	if _, err := settingsUI(ctx, s.panel, func() (bool, error) { return true, nil }); err != nil {
		return "", err
	}
	if kind != "directory" && kind != "executable" {
		return "", errors.New("invalid path picker")
	}
	if !s.mu.TryLock() {
		return "", errors.New("Working…")
	}
	defer s.mu.Unlock()
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: mygo.CallerWindow(ctx), Directory: kind == "directory", DefaultPath: current})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
