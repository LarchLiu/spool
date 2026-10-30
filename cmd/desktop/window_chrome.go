package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var titlebarChanged = mygo.NewEvent[windowChrome]("spool:titlebar")
var desktopAppearance = mygo.NewEvent[string]("spool:appearance")
var desktopUpdateAvailable = mygo.NewEvent[bool]("spool:update-available")

const desktopWindowPreload = `Object.defineProperty(window, "spoolDesktop", {value: true});
window.mygo?.on("spool:notification-open", target => {
  window.spoolNotificationTarget = target;
  window.dispatchEvent(new Event("spool:notification-changed"));
});
window.mygo?.on("spool:update-available", available => {
  const next = available === true;
  if (window.spoolUpdateAvailable === next) return;
  window.spoolUpdateAvailable = next;
  window.dispatchEvent(new Event("spool:update-changed"));
});
if (window.mygo?.platform === "win32") {
  window.mygo.on("spool:appearance", theme => {
    if (theme !== "light" && theme !== "dark") return;
    window.spoolDesktopTheme = theme;
    window.dispatchEvent(new Event("spool:appearance-changed"));
  });
}
if (window.mygo?.platform === "darwin") {
  let left = 80;
  const apply = () => {
    const root = document.documentElement;
    if (!root) return;
    root.dataset.desktopPlatform = "darwin";
    root.style.setProperty("--desktop-controls-left", left + "px");
  };
  window.mygo.on("spool:titlebar", value => {
    if (typeof value?.left !== "number" || !Number.isFinite(value.left) || value.left < 0) return;
    left = value.left;
    apply();
  });
  apply();
  document.addEventListener("DOMContentLoaded", apply, {once: true});
}`

type windowChrome struct {
	Left float32 `json:"left"`
}

func configureWindowChrome(opts *mygo.WindowOptions, platform string) {
	if platform == "darwin" {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.TrafficLightPosition = &mygo.Point{X: 16, Y: 20}
	}
}

func (p *panel) syncWindowChrome(bar ui.TitleBar) {
	if runtime.GOOS != "darwin" || p.windowChrome.Left == bar.Left {
		return
	}
	// Embedded WebViews do not receive MyGo's window-titlebar CSS variables.
	// Forward the actual inset, including zero when traffic lights hide in fullscreen.
	p.windowChrome.Left = bar.Left
	for _, view := range []*mygo.WebView{p.chatView, p.settingsView} {
		p.sendWindowChrome(view)
	}
}

func (p *panel) sendWindowChrome(view *mygo.WebView) {
	if runtime.GOOS == "darwin" && view != nil {
		_ = titlebarChanged.EmitPage(view.Page(), p.windowChrome)
	}
}

func (p *panel) sendUpdateAvailability(view *mygo.WebView) {
	if view == nil || p.updates == nil {
		return
	}
	state := p.updates.State()
	_ = desktopUpdateAvailable.EmitPage(view.Page(), state.Enabled && state.Version != "")
}

func (p *panel) sendDesktopAppearance(view *mygo.WebView) {
	if runtime.GOOS != "windows" || view == nil {
		return
	}
	theme := "light"
	if mygo.Theme.IsDark() {
		theme = "dark"
	}
	_ = desktopAppearance.EmitPage(view.Page(), theme)
}

func (p *panel) broadcastDesktopAppearance() {
	// MyGo 0.6.4 updates top-level WebView2 windows, but not embedded views.
	for _, view := range []*mygo.WebView{p.chatView, p.settingsView} {
		p.sendDesktopAppearance(view)
	}
}
