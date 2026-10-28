package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"spool/internal/i18n"
)

func (p *panel) tr(key string, values ...string) string {
	locale := p.preferences.Language
	if locale == "" || locale == "system" {
		locale = mygo.App.Locale()
	}
	return i18n.Translate(locale, key, values...)
}

func (p *panel) applyPresentation() {
	if p.appliedPreferences == p.preferences {
		return
	}
	mygo.Theme.SetSource(mygo.ThemeSource(p.preferences.Theme))
	p.broadcastDesktopAppearance()
	locale := p.preferences.Language
	if locale == "system" {
		locale = ""
	}
	mygo.App.SetLocale(locale)
	p.appliedPreferences = p.preferences
	p.updateNativeLabels()
}

func (p *panel) updateNativeLabels() {
	p.updateWindowTitle()
	if p.tray != nil {
		p.tray.SetMenu(p.trayMenu())
		p.tray.SetToolTip("Spool · " + p.tr("Web service"))
	}
}

func (p *panel) updateWindowTitle() {
	if p.window == nil {
		return
	}
	p.window.SetTitle(p.windowTitle())
}

func (p *panel) windowTitle() string {
	section := "Control Center"
	if p.tab == "Chat" {
		section = "Chat"
	}
	return mygo.App.Name() + " · " + p.tr(section)
}

func (p *panel) trayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: p.tr("Open Chat"), Click: func(*mygo.MenuItem, *mygo.Window) { p.openChat() }},
		{Label: p.tr("Open in Browser"), Click: func(*mygo.MenuItem, *mygo.Window) { p.openBrowser() }},
		{Label: p.tr("Settings…"), Click: func(*mygo.MenuItem, *mygo.Window) { p.showControlCenter() }},
		{Label: p.tr("Check for updates"), Click: func(*mygo.MenuItem, *mygo.Window) {
			p.showUpdates()
			go p.updates.Check()
		}},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

func (p *panel) appearanceView(c *ui.Context) {
	ui.Text(c, p.tr("Changes apply immediately and are saved in SQLite.")).Wrap().FontSize(12)
	ui.Text(c, p.tr("Theme")).Bold()
	themes := []string{p.tr("Light"), p.tr("Dark"), p.tr("Follow system")}
	values := []string{"light", "dark", "system"}
	selected := themes[2]
	for i, value := range values {
		if p.preferences.Theme == value {
			selected = themes[i]
		}
	}
	previous := selected
	ui.Select(c, &selected, themes).Label(p.tr("Theme"))
	if previous != selected && !p.busy {
		for i, label := range themes {
			if selected == label {
				next := p.preferences
				next.Theme = values[i]
				p.action(func() error { return p.service.Store.SaveUIPreferences("", next) })
				break
			}
		}
	}
	ui.Text(c, p.tr("Language")).Bold()
	languages := []string{p.tr("Follow system"), "简体中文", "English", "日本語"}
	locales := []string{"system", "zh-CN", "en", "ja"}
	selected = languages[0]
	for i, value := range locales {
		if p.preferences.Language == value {
			selected = languages[i]
		}
	}
	previous = selected
	ui.Select(c, &selected, languages).Label(p.tr("Language"))
	if previous != selected && !p.busy {
		for i, label := range languages {
			if selected == label {
				next := p.preferences
				next.Language = locales[i]
				p.action(func() error { return p.service.Store.SaveUIPreferences("", next) })
				break
			}
		}
	}
}
