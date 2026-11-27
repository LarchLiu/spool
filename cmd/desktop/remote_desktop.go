package main

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo"
)

const desktopRemoteDesktopPath = "/__desktop/remote-desktop"
const desktopRemoteDesktopClosePath = "/__desktop/remote-desktop-close"
const desktopRemoteDesktopConnectedPath = "/__desktop/remote-desktop-connected"

type remoteDesktopState struct {
	Connected bool   `json:"connected"`
	Token     string `json:"token,omitempty"`
}

var desktopRemoteDesktopState = mygo.NewEvent[remoteDesktopState]("spool:remote-desktop")
var desktopRemoteDesktopServer = mygo.NewEvent[string]("spool:remote-desktop-server")

const desktopRemoteDesktopPreload = `window.mygo?.on("spool:remote-desktop", connected => {
  window.dispatchEvent(new CustomEvent("spool:remote-desktop", {detail: connected}));
});`

func (p *panel) remoteDesktopServerName(id string) string {
	server, _ := p.servers.find(id)
	if id == localDesktopServer {
		server.ServerName = p.settings.ServerName
	}
	return server.displayName()
}

func (p *panel) remoteDesktopTitle(id string) string {
	return "Spool · " + p.remoteDesktopServerName(id) + " · " + p.tr("Remote desktop")
}

func (p *panel) syncRemoteDesktopTitle(id string, chat *desktopServerChat) {
	if chat.remoteDesktop != nil && !chat.remoteDesktop.IsDestroyed() {
		chat.remoteDesktop.SetTitle(p.remoteDesktopTitle(id))
		_ = desktopRemoteDesktopServer.EmitPage(chat.remoteDesktop.Page(), p.remoteDesktopServerName(id))
	}
}

func remoteDesktopConnectedNavigation(address, target string) (string, bool) {
	if !chatNavigationAllowed(address, target) {
		return "", false
	}
	u, _ := url.Parse(target)
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.Path != desktopRemoteDesktopConnectedPath || u.RawPath != "" || u.Fragment != "" ||
		len(params) != 1 || len(params["token"]) != 1 || params.Get("token") == "" || len(params.Get("token")) > 256 {
		return "", false
	}
	return params.Get("token"), true
}

func remoteDesktopPageURL(address string) string {
	u, err := url.Parse(address)
	if err != nil || !chatNavigationAllowed(address, address) {
		return ""
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/remote-desktop.html"
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	u.ForceQuery = false
	return u.String()
}

func closeDesktopRemoteDesktop(chat *desktopServerChat) {
	if chat.remoteDesktop != nil {
		chat.remoteDesktop.Close()
	}
}

func (p *panel) openRemoteDesktop(id string, chat *desktopServerChat) {
	if p.quitting || p.serverChats[id] != chat {
		return
	}
	if window := chat.remoteDesktop; window != nil && !window.IsDestroyed() {
		window.Show()
		if window.IsMinimized() {
			window.Restore()
		}
		window.Focus()
		return
	}
	address := remoteDesktopPageURL(chat.address)
	if address == "" {
		return
	}
	address += "?" + url.Values{"server": {p.remoteDesktopServerName(id)}}.Encode()
	pageOptions := desktopPageOptions(desktopWindowPreload + `
window.mygo?.on("spool:remote-desktop-server", name => {
  window.dispatchEvent(new CustomEvent("spool:remote-desktop-server", {detail: name}));
});`)
	pageOptions.Session = chat.session
	window := mygo.NewWindow(mygo.WindowOptions{
		Title: p.remoteDesktopTitle(id),
		Width: 1280, Height: 850, MinWidth: 480, MinHeight: 320,
		BackgroundColor: "light-dark(#ffffff, #242423)",
		Page:            pageOptions,
	})
	chat.remoteDesktop = window
	window.OnClosed(func() {
		if chat.remoteDesktop != window {
			return
		}
		chat.remoteDesktop = nil
		if chat.view != nil && !chat.view.IsDestroyed() {
			_ = desktopRemoteDesktopState.EmitPage(chat.view.Page(), remoteDesktopState{})
		}
	})
	page := window.Page()
	page.OnPageTitleUpdated(func(event *mygo.TitleEvent) {
		event.PreventDefault()
		window.SetTitle(p.remoteDesktopTitle(id))
	})
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		if failure, ok := desktopRenderErrorNavigation(address, event.URL); ok {
			event.PreventDefault()
			logDesktopRenderFailure("remote-desktop:"+id, failure)
			return
		}
		if chatReservedNavigation(address, event.URL, desktopRemoteDesktopClosePath) {
			event.PreventDefault()
			window.Close()
			return
		}
		if token, ok := remoteDesktopConnectedNavigation(address, event.URL); ok {
			event.PreventDefault()
			if p.serverChats[id] == chat && chat.remoteDesktop == window {
				_ = desktopRemoteDesktopState.EmitPage(chat.view.Page(), remoteDesktopState{Connected: true, Token: token})
			}
			return
		}
		if !chatNavigationAllowed(address, event.URL) {
			event.PreventDefault()
		}
	})
	page.SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
		p.openChatLink(request.URL)
		return nil
	})
	if err := page.LoadURL(address); err != nil {
		window.Close()
		p.chatError(err)
	}
}
