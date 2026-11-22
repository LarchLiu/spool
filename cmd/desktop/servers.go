package main

import (
	"context"
	"errors"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

const desktopServersPath = "/__desktop/servers"
const desktopServerStatePath = "/__desktop/server-state"
const desktopServerNamePath = "/__desktop/server-name"
const desktopServerConnectPath = "/__desktop/server-connect"
const desktopServerReadyPath = "/__desktop/server-ready"
const desktopSidebarWidthPath = "/__desktop/sidebar-width"

type desktopServerChat struct {
	view           *mygo.WebView
	address        string
	loaded         bool
	failed         bool
	status         string
	statusRevision uint64
	retrying       bool
	lastRetry      time.Time
}

func (chat *desktopServerChat) setStatus(status string) bool {
	if chat.status == status {
		return false
	}
	chat.status = status
	chat.statusRevision++
	return true
}

type desktopServerTab struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Local  bool   `json:"local"`
	Status string `json:"status"`
}

type desktopServerTabs struct {
	Entries      []desktopServerTab    `json:"entries"`
	Selected     string                `json:"selected"`
	Selection    string                `json:"selection"`
	SidebarWidth int                   `json:"sidebar_width"`
	Transition   *desktopTabTransition `json:"transition,omitempty"`
}

// The incoming cached page continues the outgoing tab indicator's animation.
// This is transient presentation state and is never written to profiles.
type desktopTabTransition struct {
	From       string   `json:"from"`
	StartedAt  int64    `json:"started_at"`
	ScrollLeft *float64 `json:"scroll_left,omitempty"`
}

type desktopServerPaint struct {
	State     string `json:"state"`
	Selection string `json:"selection"`
}

var desktopServersChanged = mygo.NewEvent[desktopServerTabs]("spool:servers")
var desktopServerSelected = mygo.NewEvent[bool]("spool:server-selected")
var desktopServerReadyAck = mygo.NewEvent[desktopServerPaint]("spool:server-ready-ack")
var desktopServerStateAck = mygo.NewEvent[string]("spool:server-state-ack")

func (p *panel) prepareDesktopServers() error {
	// OpenStore resolves relative paths and symlinks. Native transports require
	// absolute directories and must share the workspace's canonical root.
	root := p.service.Store.Root()
	p.serversPath = filepath.Join(root, "desktop-servers.json")
	var err error
	p.servers, err = readDesktopServers(p.serversPath)
	if err != nil {
		return err
	}
	p.serverGateway, err = startDesktopServerGateway(p.devFrontend, p.servers)
	if err != nil {
		return err
	}
	p.serverNetworks = newDesktopServerNetworks(filepath.Join(root, "desktop-connections"))
	p.serverGateway.networks = p.serverNetworks
	p.serverGateway.update(p.servers)
	return nil
}

func (p *panel) selectedDesktopServer() string {
	if p.servers.Selected == "" {
		return localDesktopServer
	}
	return p.servers.Selected
}

func (p *panel) desktopServerTabs() desktopServerTabs {
	tabs := desktopServerTabs{Selected: p.selectedDesktopServer(), SidebarWidth: p.servers.SidebarWidth, Transition: p.serverTabTransition}
	tabs.Selection = p.desktopSelectionToken()
	if tabs.SidebarWidth == 0 {
		tabs.SidebarWidth = 298
	}
	for _, server := range p.servers.Entries {
		status := "disconnected"
		if chat := p.serverChats[server.ID]; server.Enabled && chat != nil && chat.status != "" {
			status = chat.status
		}
		if server.ID == localDesktopServer {
			server.ServerName = p.settings.ServerName
		}
		tabs.Entries = append(tabs.Entries, desktopServerTab{server.ID, server.displayName(), server.ID == localDesktopServer, status})
	}
	return tabs
}

func (p *panel) desktopSelectionToken() string {
	var started int64
	if p.serverTabTransition != nil {
		started = p.serverTabTransition.StartedAt
	}
	return p.selectedDesktopServer() + ":" + strconv.FormatInt(started, 10)
}

func (p *panel) recordDesktopServerName(id, name string) {
	name = strings.TrimSpace(name)
	if id == localDesktopServer {
		if p.settings.ServerName == name {
			return
		}
		p.settings.ServerName = name
	} else {
		next := p.servers
		next.Entries = append([]desktopServer(nil), next.Entries...)
		found := false
		for i := range next.Entries {
			if next.Entries[i].ID == id && next.Entries[i].ServerName != name {
				next.Entries[i].ServerName, found = name, true
			}
		}
		if !found || writeDesktopServers(p.serversPath, next) != nil {
			return
		}
		p.servers = next
	}
	p.sendDesktopServers()
}

func (p *panel) sendDesktopServers() {
	if p.chatView != nil {
		_ = desktopServersChanged.EmitPage(p.chatView.Page(), p.desktopServerTabs())
	}
}

func (p *panel) setDesktopServers(next desktopServers) error {
	if err := writeDesktopServers(p.serversPath, next); err != nil {
		return err
	}
	previous := p.servers
	p.servers = next
	if p.serverGateway != nil {
		p.serverGateway.update(next)
	}
	for id, chat := range p.serverChats {
		old, _ := previous.find(id)
		server, exists := next.find(id)
		if !exists || !server.Enabled || old.Enabled != server.Enabled || old.Address != server.Address || old.Mode != server.Mode {
			if p.chatPresentedView == chat.view {
				p.chatPresentedView = nil
			}
			if chat.view != nil {
				chat.view.Destroy()
			}
			if p.chatView == chat.view {
				p.chatView, p.chatURL, p.chatLoaded = nil, "", false
			}
			delete(p.serverChats, id)
		}
	}
	if p.serverNetworks != nil {
		p.serverNetworks.update(next)
	}
	p.sendDesktopServers()
	return nil
}

// Probe alternative routes without account cookies or replaying a failed write.
// A successful route gets its own origin-scoped sign-in, just as on Android.
func (p *panel) retryDesktopServer(id string) {
	server, ok := p.servers.find(id)
	chat := p.serverChats[id]
	if p.quitting || !ok || !server.Enabled || !server.Automatic || p.selectedDesktopServer() != id || p.serverNetworks == nil || chat == nil || chat.status != "error" || chat.retrying || time.Since(chat.lastRetry) < 5*time.Second {
		return
	}
	chat.retrying, chat.lastRetry = true, time.Now()
	revision := chat.statusRevision
	p.serverNetworks.run(func(lifetime context.Context) {
		var chosen string
		for _, mode := range []string{"direct", "tailscale", "relay"} {
			candidate := server.connection(mode)
			if mode == server.Mode || candidate.Address == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(lifetime, 4*time.Second)
			reachable := p.serverNetworks.reachable(ctx, candidate)
			cancel()
			if reachable {
				chosen = mode
				break
			}
		}
		p.window.Update(func() {
			if p.applyDesktopServerRetry(server, chat, revision, chosen) {
				p.openChat()
			}
		})
	})
}

func (p *panel) applyDesktopServerRetry(server desktopServer, chat *desktopServerChat, revision uint64, chosen string) bool {
	chat.retrying = false
	current, exists := p.servers.find(server.ID)
	// A probe belongs to one failure on one page. A recovered connection, a
	// later failure, or a replacement page must keep its own workspace state.
	if p.quitting || !exists || current != server || p.selectedDesktopServer() != server.ID || chosen == "" ||
		p.serverChats[server.ID] != chat || chat.status != "error" || chat.statusRevision != revision {
		return false
	}
	next, err := p.servers.save(server.connection(chosen))
	return err == nil && p.setDesktopServers(next) == nil
}

func (p *panel) selectDesktopServer(id string) error {
	return p.selectDesktopServerWithScroll(id, nil)
}

func (p *panel) selectDesktopServerWithScroll(id string, scrollLeft *float64) error {
	_, ok := p.servers.find(id)
	if !ok {
		return errors.New("Server not found.")
	}
	next := p.servers
	next.Selected = id
	previousTransition := p.serverTabTransition
	if previous := p.selectedDesktopServer(); previous != id {
		started := time.Now().UnixMilli()
		if previousTransition != nil && started <= previousTransition.StartedAt {
			started = previousTransition.StartedAt + 1
		}
		p.serverTabTransition = &desktopTabTransition{From: previous, StartedAt: started, ScrollLeft: scrollLeft}
	}
	// A local service start may still be in flight when another tab is chosen.
	p.chatStarting = false
	if err := p.setDesktopServers(next); err != nil {
		p.serverTabTransition = previousTransition
		return err
	}
	p.openChat()
	if p.chatView != nil {
		_ = desktopServerSelected.EmitPage(p.chatView.Page(), true)
	}
	return nil
}

func (p *panel) connectDesktopServer() error {
	server, ok := p.servers.find(p.selectedDesktopServer())
	if !ok {
		return errors.New("Server not found.")
	}
	if server.ID != localDesktopServer && !server.Enabled {
		server.Enabled = true
		next, err := p.servers.save(server)
		if err != nil {
			return err
		}
		if err := p.setDesktopServers(next); err != nil {
			return err
		}
	}
	if chat := p.serverChats[server.ID]; chat != nil {
		chat.failed = true
		chat.setStatus("connecting")
	}
	if p.selectedDesktopServer() == server.ID {
		p.chatFailed = true
	}
	p.openChat()
	return nil
}

type desktopTabSelection struct {
	ID         string
	ScrollLeft *float64
}

func desktopServerNavigation(address, target string) (desktopTabSelection, bool) {
	if !chatNavigationAllowed(address, target) {
		return desktopTabSelection{}, false
	}
	u, _ := url.Parse(target)
	if u.Path != desktopServersPath || u.RawPath != "" || u.Fragment != "" {
		return desktopTabSelection{}, false
	}
	if u.RawQuery == "" && !u.ForceQuery {
		return desktopTabSelection{}, true
	}
	params, err := url.ParseQuery(u.RawQuery)
	selection := desktopTabSelection{ID: params.Get("id")}
	if err != nil || selection.ID == "" || len(params["id"]) != 1 {
		return desktopTabSelection{}, false
	}
	if values, ok := params["scroll_left"]; ok {
		if len(values) != 1 || len(params) != 2 {
			return desktopTabSelection{}, false
		}
		scroll, err := strconv.ParseFloat(values[0], 64)
		if err != nil || math.IsNaN(scroll) || math.IsInf(scroll, 0) || scroll < 0 || scroll > 1e7 {
			return desktopTabSelection{}, false
		}
		selection.ScrollLeft = &scroll
	} else if len(params) != 1 {
		return desktopTabSelection{}, false
	}
	return selection, true
}

func desktopServerStateNavigation(address, target string) (string, bool) {
	return desktopServerStatusNavigation(address, target, desktopServerStatePath)
}

func desktopServerReadyNavigation(address, target string) (desktopServerPaint, bool) {
	if !chatNavigationAllowed(address, target) {
		return desktopServerPaint{}, false
	}
	u, _ := url.Parse(target)
	if u.Path != desktopServerReadyPath || u.RawPath != "" || u.Fragment != "" {
		return desktopServerPaint{}, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	paint := desktopServerPaint{State: params.Get("state"), Selection: params.Get("selection")}
	return paint, err == nil && len(params) == 2 && len(params["state"]) == 1 && len(params["selection"]) == 1 && validDesktopServerStatus(paint.State) && len(paint.Selection) > 0 && len(paint.Selection) <= 256
}

func desktopServerStatusNavigation(address, target, path string) (string, bool) {
	return desktopServerHint(address, target, path, "state", func(state string) bool {
		return validDesktopServerStatus(state)
	})
}

func validDesktopServerStatus(state string) bool {
	return state == "connected" || state == "connecting" || state == "login" || state == "error" || state == "disconnected"
}

func (p *panel) recordDesktopServerState(id, status string) {
	state := p.serverChats[id]
	if state == nil || !state.setStatus(status) {
		return
	}
	p.sendDesktopServers()
	if status == "error" {
		if server, ok := p.servers.find(id); ok && p.serverNetworks != nil {
			p.serverNetworks.networkChanged(server)
		}
		p.retryDesktopServer(id)
	}
}

func desktopServerNameNavigation(address, target string) (string, bool) {
	return desktopServerHint(address, target, desktopServerNamePath, "name", func(name string) bool {
		return utf8.ValidString(name) && utf8.RuneCountInString(name) <= 4096 && !strings.ContainsRune(name, 0)
	})
}

func desktopSidebarWidthNavigation(address, target string) (int, bool) {
	value, ok := desktopServerHint(address, target, desktopSidebarWidthPath, "width", func(value string) bool {
		width, err := strconv.Atoi(value)
		return err == nil && width >= 220 && width <= 480
	})
	width, _ := strconv.Atoi(value)
	return width, ok
}

func (p *panel) recordDesktopSidebarWidth(width int) error {
	if width == p.servers.SidebarWidth {
		return nil
	}
	next := p.servers
	next.SidebarWidth = width
	if err := writeDesktopServers(p.serversPath, next); err != nil {
		return err
	}
	p.servers = next
	p.sendDesktopServers()
	return nil
}

func desktopServerHint(address, target, path, key string, valid func(string) bool) (string, bool) {
	if !chatNavigationAllowed(address, target) {
		return "", false
	}
	u, _ := url.Parse(target)
	if u.Path != path || u.RawPath != "" || u.Fragment != "" {
		return "", false
	}
	if path == desktopServersPath && u.RawQuery == "" && !u.ForceQuery {
		return "", true
	}
	params, err := url.ParseQuery(u.RawQuery)
	value := params.Get(key)
	return value, err == nil && len(params) == 1 && len(params[key]) == 1 && valid(value)
}
