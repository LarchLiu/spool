package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var titlebarChanged = mygo.NewEvent[windowChrome]("spool:titlebar")

const desktopWindowPreload = `Object.defineProperty(window, "spoolDesktop", {value: true});
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
