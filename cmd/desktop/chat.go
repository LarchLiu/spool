package main

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const desktopSettingsPath = "/__desktop/settings"

func (p *panel) openChat() {
	if p.quitting {
		return
	}
	p.selectTab("Chat")
	p.showMainWindow()
	if p.chatStarting {
		return
	}
	p.chatStarting = true
	go func() {
		err := p.service.Start()
		var address string
		if err == nil {
			address, err = p.service.LocalWebURL()
		}
		p.window.Update(func() {
			p.chatStarting = false
			if p.quitting {
				return
			}
			if err == nil {
				err = p.loadChatView(address)
			}
			if err != nil {
				p.chatError(err)
			}
			p.status = p.service.Status()
		})
	}()
}

func chatWebViewOptions() mygo.WebViewOptions {
	return mygo.WebViewOptions{
		BackgroundColor: "light-dark(#ffffff, #242423)",
		// This is only a presentation hint. It grants no native methods or API
		// privileges; login, project authorization and CSRF remain unchanged.
		Page: mygo.PageOptions{PreloadScript: `Object.defineProperty(window, "spoolDesktop", {value: true});`},
	}
}

func (p *panel) loadChatView(address string) error {
	if p.chatView == nil {
		if !mygo.WebViewAvailable() {
			return fmt.Errorf("Desktop chat requires a system WebView. Install WebView2 (Windows) or WebKitGTK (Linux), or use Open in Browser.")
		}
		view, err := p.window.NewWebView(chatWebViewOptions())
		if err != nil {
			return err
		}
		p.chatView = view
		page := view.Page()
		// Register guards before the first navigation. Never trust additional
		// origins or load external content into a privileged desktop window.
		page.OnWillNavigate(func(e *mygo.NavigateEvent) {
			if chatSettingsNavigation(p.chatURL, e.URL) {
				e.PreventDefault()
				p.window.Update(p.showControlCenter)
				return
			}
			if chatNavigationAllowed(p.chatURL, e.URL) {
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
		page.OnDidFinishLoad(func() {
			p.chatFailed = false
			if p.tab == "Chat" {
				view.Focus()
			}
		})
		page.OnDidFailLoad(func(err *mygo.LoadError) {
			p.chatFailed = true
			p.chatError(err)
		})
		page.OnRenderProcessGone(func(reason string) {
			p.chatFailed = true
			p.chatError(fmt.Errorf("%s: %s", p.tr("Chat renderer stopped. Reopen Chat to reload."), reason))
		})
	}
	if p.chatURL != address || p.chatFailed {
		p.chatURL = address
		p.chatFailed = false
		if err := p.chatView.Page().LoadURL(address); err != nil {
			p.chatFailed = true
			return err
		}
	}
	return nil
}

func (p *panel) chatContent(c *ui.Context) {
	if p.chatView != nil {
		ui.WebView(c, p.chatView).Fill()
		return
	}
	ui.Column(c).Fill().Padding(28).Gap(16).Children(func() {
		ui.Text(c, p.tr("Chat")).Bold().FontSize(27)
		ui.Text(c, p.tr("Opening chat…")).Wrap()
		if ui.Button(c, p.tr("Settings")).Clicked() {
			p.showControlCenter()
		}
	})
}

func (p *panel) selectTab(tab string) {
	p.tab = tab
	p.message = ""
	if p.window != nil {
		p.updateWindowTitle()
		p.window.Invalidate()
	}
}

func (p *panel) showMainWindow() {
	p.window.Show()
	if p.window.IsMinimized() {
		p.window.Restore()
	}
	p.window.Focus()
}

func (p *panel) showControlCenter() {
	if p.tab == "Chat" {
		p.selectTab("Service")
	}
	p.showMainWindow()
}

func (p *panel) chatError(err error) {
	log.Printf("event=desktop_chat_failed error=%q", err)
	p.window.Update(func() {
		p.message = p.tr("Could not open desktop chat.") + " " + p.tr(err.Error())
		p.tab = "Service"
		p.updateWindowTitle()
		p.showControlCenter()
	})
}

func (p *panel) openChatLink(address string) {
	if !chatExternalLinkAllowed(address) {
		return
	}
	go func() {
		if err := mygo.Shell.OpenExternal(address); err != nil {
			p.chatError(err)
		}
	}()
}

func (p *panel) openBrowser() {
	go func() {
		if err := mygo.Shell.OpenExternal(p.service.WebURL()); err != nil {
			p.chatError(err)
		}
	}()
}

func chatNavigationAllowed(address, target string) bool {
	base, err := url.Parse(address)
	if err != nil || base.Host == "" || base.User != nil {
		return false
	}
	u, err := url.Parse(target)
	return err == nil && u.User == nil && u.Opaque == "" &&
		(u.Scheme == "http" || u.Scheme == "https") &&
		strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// This reserved same-origin link only changes native presentation. It is not
// an HTTP settings API and does not expose any administrative operations.
func chatSettingsNavigation(address, target string) bool {
	if !chatNavigationAllowed(address, target) {
		return false
	}
	u, _ := url.Parse(target)
	return u.Path == desktopSettingsPath && u.RawPath == "" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func chatExternalLinkAllowed(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Host != "" && u.User == nil && u.Opaque == "" &&
		(u.Scheme == "http" || u.Scheme == "https")
}
