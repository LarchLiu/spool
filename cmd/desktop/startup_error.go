package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"spool/internal/diagnostics"
	"spool/internal/i18n"
)

type startupFailure struct {
	root, version, logPath string
	cause                  error
	retry, showData        func()
	showLogs, quit         func()
	copy                   func(string)
}

func (s *startupFailure) hint() string {
	message := strings.ToLower(s.cause.Error())
	switch {
	case strings.Contains(message, "unsupported database schema"):
		return "This database was created by a newer Spool version. Install a compatible newer app; do not change the schema version or delete the database."
	case strings.Contains(message, "another running instance"), strings.Contains(message, "locked"), strings.Contains(message, "busy"):
		return "Stop other Spool desktop/server instances using this directory. After a crash, wait at least 30 seconds, then retry."
	case strings.Contains(message, "permission"), strings.Contains(message, "access is denied"), strings.Contains(message, "read-only"):
		return "Check that the data directory is writable by your account. You can explicitly choose another directory with -data-dir."
	default:
		return "Keep the database and its sidecar files intact. Check the error and logs before restoring a backup or changing the data directory."
	}
}

func (s *startupFailure) report() string {
	return fmt.Sprintf("Spool %s\nData directory: %s\nDatabase: %s\nLog: %s\nError: %s", s.version, s.root, filepath.Join(s.root, "db", "spool.sqlite"), s.logPath, s.cause)
}

func (s *startupFailure) view(c *ui.Context, locale string) {
	tr := func(key string) string { return i18n.Translate(locale, key) }
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(28).Gap(16).Children(func() {
			ui.Text(c, tr("Spool could not open its data")).Bold().FontSize(25).Wrap()
			ui.Text(c, tr("The app is still running. The Web service and agents have not been started.")).Wrap()
			ui.Text(c, s.cause.Error()).Selectable().Wrap().FontSize(13)
			ui.Text(c, tr(s.hint())).Wrap()
			ui.Text(c, tr("Data directory")+": "+s.root).Selectable().Wrap().FontSize(12)
			ui.Text(c, "SQLite: "+filepath.Join(s.root, "db", "spool.sqlite")).Selectable().Wrap().FontSize(12)
			if s.logPath != "" {
				ui.Text(c, tr("Application log")+": "+s.logPath).Selectable().Wrap().FontSize(12)
			} else {
				ui.Text(c, tr("File logging is unavailable. Diagnostic output is sent to stderr.")).Wrap()
			}
			ui.Row(c).Gap(10).Children(func() {
				if ui.PrimaryButton(c, tr("Retry startup")).Clicked() {
					s.retry()
				}
				if ui.Button(c, tr("Show data directory")).Clicked() {
					s.showData()
				}
				if ui.Button(c, tr("Show application log")).Disabled(s.logPath == "").Clicked() {
					s.showLogs()
				}
			})
			ui.Row(c).Gap(10).Children(func() {
				if ui.Button(c, tr("Copy diagnostics")).Clicked() {
					s.copy(s.report())
				}
				if ui.Button(c, tr("Quit")).Clicked() {
					s.quit()
				}
			})
		})
	})
}

func showStartupFailure(root string, cause error, logs *diagnostics.Session) error {
	log.Printf("event=startup_failed error=%q", cause)
	screen := &startupFailure{root: root, version: mygo.App.Version(), cause: cause,
		retry:    func() { log.Print("event=startup_retry_requested"); mygo.App.Relaunch() },
		showData: func() { mygo.Shell.ShowItemInFolder(root) },
		quit:     mygo.App.Quit, copy: mygo.Clipboard.WriteText,
	}
	if logs != nil {
		screen.logPath = logs.Path
		screen.showLogs = func() { mygo.Shell.ShowItemInFolder(logs.Path) }
	}
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{Title: mygo.App.Name(), Width: 800, Height: 620, MinWidth: 720, MinHeight: 540, Content: ui.View(func(c *ui.Context) { screen.view(c, mygo.App.Locale()) })})
		window.OnClose(func(*mygo.CloseEvent) { mygo.App.Quit() })
		mygo.App.OnSecondInstance(func([]string, string) { window.Show(); window.Focus() })
		mygo.App.OnActivate(func(bool) { window.Show(); window.Focus() })
		window.Show()
		window.Focus()
		log.Print("event=startup_error_window_ready")
	})
	mygo.App.OnQuit(func() { log.Print("event=startup_error_window_closed") })
	return mygo.App.Run()
}
