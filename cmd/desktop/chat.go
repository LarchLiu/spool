package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	chatmodel "spool/internal/model"
)

const desktopSettingsPath = "/__desktop/settings"
const desktopSessionChangedPath = "/__desktop/session-changed"
const desktopViewPath = "/__desktop/view"

func (p *panel) startOnLaunch() {
	if p.selectedDesktopServer() != localDesktopServer {
		p.tab = "Chat"
	}
	if !p.settings.AutoStart {
		return
	}
	if err := p.service.Start(); err != nil {
		p.message = err.Error()
		log.Printf("event=service_autostart_failed error=%q", err)
		return
	}
	p.status = p.service.Status()
	p.tab = "Chat"
}

func (p *panel) openChat() {
	if p.quitting || p.window == nil {
		return
	}
	p.selectTab("Chat")
	p.showMainWindow()
	id := p.selectedDesktopServer()
	p.chatGeneration++
	generation := p.chatGeneration
	if id != localDesktopServer {
		server, ok := p.servers.find(id)
		if !ok || p.serverGateway == nil {
			return
		}
		if err := p.loadChatView(p.serverGateway.url + desktopServerMount(server)); err != nil {
			p.chatError(err)
		}
		return
	}
	if p.service.Status().Running {
		address, err := p.service.LocalWebURL()
		if err == nil {
			err = p.loadChatView(address)
		}
		if err != nil {
			p.chatError(err)
		}
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
			if generation != p.chatGeneration {
				return
			}
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
		Page: mygo.PageOptions{PreloadScript: desktopWindowPreload + "\n" + desktopRemoteDesktopPreload},
	}
}

func (p *panel) loadChatView(address string) error {
	id := p.selectedDesktopServer()
	if err := p.loadDesktopServerChat(id, address); err != nil {
		return err
	}
	state := p.serverChats[id]
	p.chatView, p.chatURL, p.chatFailed, p.chatLoaded = state.view, state.address, state.failed, state.loaded
	if state.renderFailed {
		// The failed App cannot send another ready hint when this cached page
		// is selected. Its recovery screen is already usable.
		p.chatPresentedView = state.view
	}
	p.sendDesktopServers()
	p.window.Invalidate()
	if p.tab == "Chat" && p.chatPresentedView == p.chatView && !p.serverDialogOpen {
		p.chatView.Focus()
	}
	p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	if id == localDesktopServer {
		p.sendSessionNotifications()
		p.sendPendingNotification()
	}
	return nil
}

// Each enabled server owns a page, including servers never selected this run.
// Loading one must not change the foreground page, selection or window focus.
func (p *panel) loadDesktopServerChat(id, address string) error {
	if p.devFrontend != nil && id == localDesktopServer {
		address = p.devFrontend.url + "/"
	}
	if p.serverChats == nil {
		p.serverChats = map[string]*desktopServerChat{}
	}
	state := p.serverChats[id]
	if state == nil {
		state = &desktopServerChat{status: "connecting", pageStatus: "connecting"}
		p.serverChats[id] = state
	}
	if state.view == nil {
		if !mygo.WebViewAvailable() {
			state.address, state.failed = address, true
			p.recordDesktopServerState(id, "error")
			return fmt.Errorf("Desktop chat requires a system WebView. Install WebView2 (Windows) or WebKitGTK (Linux), or use Open in Browser.")
		}
		options := chatWebViewOptions()
		tabs, _ := json.Marshal(p.desktopServerTabsFor(id))
		options.Page.PreloadScript += "\nwindow.spoolServers = " + string(tabs) + ";"
		view, err := p.window.NewWebView(options)
		if err != nil {
			state.address, state.failed = address, true
			p.recordDesktopServerState(id, "error")
			return err
		}
		state.view = view
		if id == p.selectedDesktopServer() {
			p.chatView = view
		}
		page := view.Page()
		// Register guards before the first navigation. Never trust additional
		// origins or load external content into a privileged desktop window.
		page.OnWillNavigate(func(e *mygo.NavigateEvent) {
			if failure, ok := desktopRenderErrorNavigation(state.address, e.URL); ok {
				e.PreventDefault()
				logDesktopRenderFailure("chat:"+id, failure)
				if failure.fatal {
					p.window.Update(func() {
						if p.quitting || p.serverChats[id] != state {
							return
						}
						state.renderFailed = true
						if p.chatView == view {
							p.chatPresentedView = view
							p.window.Invalidate()
							if p.tab == "Chat" && !p.serverDialogOpen {
								view.Focus()
							}
						}
					})
				}
				return
			}
			if paint, ok := desktopServerReadyNavigation(state.address, e.URL); ok {
				e.PreventDefault()
				p.window.Update(func() {
					if p.quitting || p.serverChats[id] != state {
						return
					}
					state.renderFailed = false
					// Acknowledge even an outgoing page so its notification retry
					// stops. Readiness hints can otherwise lose a navigation race
					// with the page's session/name/status notifications.
					_ = desktopServerReadyAck.EmitPage(view.Page(), paint)
					// Cached background pages still report their connection health.
					// Only presenting a page depends on the current selection token.
					p.recordDesktopServerConnection(id, paint.desktopServerConnection)
					if paint.Selection != p.desktopSelectionToken() {
						return
					}
					if p.chatView == view {
						p.chatPresentedView = view
						p.window.Invalidate()
						p.setDesktopNotificationFocused(p.desktopNotificationFocused())
						if p.tab == "Chat" && !p.serverDialogOpen {
							view.Focus()
						}
					}
				})
				return
			}
			if name, ok := desktopServerNameNavigation(state.address, e.URL); ok {
				e.PreventDefault()
				p.window.Update(func() {
					if p.serverChats[id] == state {
						p.recordDesktopServerName(id, name)
					}
				})
				return
			}
			if connection, ok := desktopServerStateNavigation(state.address, e.URL); ok {
				e.PreventDefault()
				p.window.Update(func() {
					if p.serverChats[id] != state {
						return
					}
					p.recordDesktopServerConnection(id, connection)
					_ = desktopServerStateAck.EmitPage(view.Page(), connection)
				})
				return
			}
			if chatReservedNavigation(state.address, e.URL, desktopFederationTargetsPath) {
				e.PreventDefault()
				p.window.Update(func() { p.refreshDesktopFederationTargets(id, state) })
				return
			}
			if request, reserved, ok := desktopFederationNavigation(state.address, e.URL); reserved {
				e.PreventDefault()
				if ok {
					p.window.Update(func() {
						if p.chatView == view && p.serverChats[id] == state {
							p.selectDesktopFederationTab(id, state, request)
						}
					})
				}
				return
			}
			// Keep the outgoing tab bar usable while its replacement renders.
			if selection, ok := desktopServerNavigation(state.address, e.URL); ok && (p.chatView == view || p.chatPresentedView == view) {
				e.PreventDefault()
				p.window.Update(func() {
					if p.quitting || p.serverChats[id] != state || (p.chatView != view && p.chatPresentedView != view) {
						return
					}
					if selection.ID == "" {
						p.showServerDialog("")
					} else if err := p.selectDesktopServerTab(selection.ID, selection.ScrollLeft); err != nil {
						p.showServerDialog(selection.ID)
					}
				})
				return
			}
			if p.chatView != view {
				// Background pages still load their own document and accept dev
				// reloads, but cannot invoke foreground navigation actions.
				u, _ := url.Parse(e.URL)
				if !chatNavigationAllowed(state.address, e.URL) || strings.HasPrefix(u.Path, "/__desktop/") {
					e.PreventDefault()
				}
				return
			}
			if width, ok := desktopSidebarWidthNavigation(state.address, e.URL); ok {
				e.PreventDefault()
				p.window.Update(func() {
					if p.chatView == view && !p.quitting {
						if err := p.recordDesktopSidebarWidth(width); err != nil {
							log.Printf("event=desktop_sidebar_width_failed error=%q", err)
						}
					}
				})
				return
			}
			if chatReservedNavigation(state.address, e.URL, desktopRemoteDesktopPath) {
				e.PreventDefault()
				p.window.Update(func() { p.openRemoteDesktop(id, state) })
				return
			}
			if chatReservedNavigation(state.address, e.URL, desktopRemoteDesktopClosePath) {
				e.PreventDefault()
				p.window.Update(func() { closeDesktopRemoteDesktop(state) })
				return
			}
			if chatReservedNavigation(p.chatURL, e.URL, desktopServerConnectPath) {
				e.PreventDefault()
				p.window.Update(func() {
					if err := p.connectDesktopServer(); err != nil {
						p.showServerDialog(p.selectedDesktopServer())
					}
				})
				return
			}
			if view, ok := desktopViewNavigation(p.chatURL, e.URL); ok {
				e.PreventDefault()
				if id != localDesktopServer {
					return
				}
				p.window.Update(func() {
					p.notificationChat = view.chat
					p.setDesktopNotificationFocused(p.desktopNotificationFocused())
					if view.sessionChanged {
						p.refreshDesktopNotificationSession()
					}
				})
				return
			}
			if chatSessionChangedNavigation(p.chatURL, e.URL) {
				e.PreventDefault()
				if id == localDesktopServer {
					p.window.Update(p.refreshDesktopNotificationSession)
				}
				return
			}
			if target, ok := attachmentAccessNavigation(p.chatURL, e.URL); ok {
				e.PreventDefault()
				if e.UserInitiated && id == localDesktopServer {
					p.window.Update(func() { p.chooseAttachmentAccess(target) })
				}
				return
			}
			if chatSettingsNavigation(p.chatURL, e.URL) {
				e.PreventDefault()
				p.window.Update(func() {
					if id == localDesktopServer {
						p.showControlCenter()
					} else {
						p.showServerDialog(id)
					}
				})
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
		page.OnDidNavigate(func(string) {
			if p.serverChats[id] == state {
				state.renderFailed = false
			}
		})
		page.OnDidFinishLoad(func() {
			if p.serverChats[id] != state {
				return
			}
			p.sendWindowChrome(view)
			p.sendDesktopAppearance(view)
			p.sendUpdateAvailability(view)
			state.failed, state.loaded = false, true
			p.learnDesktopServerIdentity(id, state)
			p.refreshDesktopFederationTargets(id, state)
			p.sendDesktopServers()
			if p.chatView != view {
				return
			}
			p.chatFailed, p.chatLoaded = false, true
			p.setDesktopNotificationFocused(p.desktopNotificationFocused())
			p.refreshDesktopNotificationSession()
			p.sendSessionNotifications()
			p.sendPendingNotification()
			if p.tab == "Chat" && p.chatPresentedView == view && !p.serverDialogOpen {
				view.Focus()
			}
		})
		page.OnDidFailLoad(func(err *mygo.LoadError) {
			if p.serverChats[id] != state {
				return
			}
			state.failed = true
			p.recordDesktopServerState(id, "error")
			if p.chatView == view {
				p.chatFailed = true
				p.chatError(err)
			}
		})
		page.OnRenderProcessGone(func(reason string) {
			p.desktopChatRendererGone(id, state, reason)
		})
	}
	if state.address != address || state.failed {
		if state.address != address {
			closeDesktopRemoteDesktop(state)
		}
		state.address, state.loaded, state.failed = address, false, false
		state.authenticated = false
		state.federationTargets = nil
		state.federationFingerprint = ""
		state.federationRevision++
		if id == p.selectedDesktopServer() {
			p.chatView, p.chatURL, p.chatFailed, p.chatLoaded = state.view, address, false, false
			p.setDesktopNotificationFocused(p.desktopNotificationFocused())
		}
		p.recordDesktopServerState(id, "connecting")
		if err := state.view.Page().LoadURL(address); err != nil {
			if p.chatView == state.view {
				p.chatFailed = true
			}
			state.failed = true
			p.recordDesktopServerState(id, "error")
			return err
		}
	}
	p.sendWindowChrome(state.view)
	p.sendDesktopAppearance(state.view)
	p.sendUpdateAvailability(state.view)
	p.sendDesktopServers()
	return nil
}

func (p *panel) chatContent(c *ui.Context) {
	if p.chatView != nil {
		if p.chatPresentedView != nil && p.chatPresentedView != p.chatView {
			// Both native views get a full frame. The outgoing page stays on top
			// until React has rendered the incoming shell and signalled ready.
			ui.Box(c).Fill().Children(func() {
				c.Key("incoming-chat")
				ui.WebView(c, p.chatView).Absolute().Top(0).Right(0).Bottom(0).Left(0)
				c.Key("presented-chat")
				ui.WebView(c, p.chatPresentedView).Absolute().Top(0).Right(0).Bottom(0).Left(0)
			})
			return
		}
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
	p.message = ""
	if p.tab == "Service" && tab != p.tab {
		p.flushServiceSetting()
	}
	p.tab = tab
	p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	if p.window != nil {
		if tab != "Chat" && p.settingsView != nil && !p.settingsFailed {
			_ = settingsSection.EmitPage(p.settingsView.Page(), tab)
		}
		p.updateWindowTitle()
		p.window.Invalidate()
	}
}

func (p *panel) showMainWindow() {
	mygo.App.Dock.Show()
	p.window.Show()
	if p.window.IsMinimized() {
		p.window.Restore()
	}
	p.window.Focus()
}

func (p *panel) showControlCenter() {
	if p.tab == "Chat" {
		if p.selectedDesktopServer() == localDesktopServer {
			p.selectTab("Service")
		} else {
			p.showServerDialog(p.selectedDesktopServer())
			return
		}
	}
	p.showMainWindow()
}

func (p *panel) chatError(err error) {
	log.Printf("event=desktop_chat_failed error=%q", err)
	p.window.Update(func() {
		if p.selectedDesktopServer() == localDesktopServer {
			p.selectTab("Service")
		} else {
			p.showServerDialog(p.selectedDesktopServer())
			return
		}
		p.message = p.tr("Could not open desktop chat.") + " " + p.tr(err.Error())
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
	address := p.service.WebURL()
	if server, ok := p.servers.find(p.selectedDesktopServer()); ok && server.ID != localDesktopServer {
		address = server.Address
		if p.serverGateway != nil && (server.Mode == "tailscale" || server.Mode == "relay") {
			address = p.serverGateway.url + desktopServerMount(server)
		}
	}
	go func() {
		if err := mygo.Shell.OpenExternal(address); err != nil {
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
	return chatReservedNavigation(address, target, desktopSettingsPath)
}

// A same-origin hint only: intercept it without loading another document or
// granting the chat access to native methods. Cookie validation stays native.
func chatSessionChangedNavigation(address, target string) bool {
	return chatReservedNavigation(address, target, desktopSessionChangedPath)
}

type desktopView struct {
	chat           string
	sessionChanged bool
}

// This hint only suppresses redundant presentation. It carries no account or
// credentials; notification authorization still uses the native cookie store.
func desktopViewNavigation(address, target string) (desktopView, bool) {
	if !chatNavigationAllowed(address, target) {
		return desktopView{}, false
	}
	u, _ := url.Parse(target)
	if u.Path != desktopViewPath || u.RawPath != "" || u.Fragment != "" {
		return desktopView{}, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(params["chat"]) != 1 || len(params) > 2 || len(params["session"]) > 1 {
		return desktopView{}, false
	}
	chat := params.Get("chat")
	if chat != "" && !chatmodel.ValidID(chat) {
		return desktopView{}, false
	}
	if len(params) == 2 && params.Get("session") != "changed" {
		return desktopView{}, false
	}
	return desktopView{chat, params.Get("session") == "changed"}, true
}

func chatReservedNavigation(address, target, path string) bool {
	if !chatNavigationAllowed(address, target) {
		return false
	}
	u, _ := url.Parse(target)
	return u.Path == path && u.RawPath == "" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func chatExternalLinkAllowed(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Host != "" && u.User == nil && u.Opaque == "" &&
		(u.Scheme == "http" || u.Scheme == "https")
}
