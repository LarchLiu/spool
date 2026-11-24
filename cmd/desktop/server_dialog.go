package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"spool/android/transport/spoolnet"
	"spool/internal/tailnet"
)

const serverDialogURL = "mygo://localhost/servers.html"

var serverDialogChanged = mygo.NewEvent[desktopServerSnapshot]("spool:server-dialog")

type desktopServerSettings struct{ panel *panel }

type desktopServerSnapshot struct {
	Server   desktopServer `json:"server"`
	Status   string        `json:"status"`
	Selected bool          `json:"selected"`
	Active   bool          `json:"active"`
}

func (p *panel) serverDialogURL() string {
	if p.devFrontend != nil {
		return p.devFrontend.url + "/servers.html"
	}
	return serverDialogURL
}

func (p *panel) serverDialogDocumentAllowed(address string) bool {
	return address == p.serverDialogURL() || p.devFrontend == nil && address == "http://mygo.localhost/servers.html"
}

func (p *panel) showServerDialog(id string) {
	if p.quitting || p.window == nil {
		return
	}
	if id != "" {
		if _, ok := p.servers.find(id); !ok || id == localDesktopServer {
			return
		}
	}
	p.showMainWindow()
	if p.serverDialog != nil && p.serverDialogRequested && p.serverDialogID == id {
		if p.serverDialogOpen {
			p.serverDialog.Focus()
		}
		return
	}
	if p.serverDialogRequested {
		p.hideServerDialog()
	}
	p.serverDialogID = id
	p.serverDialogRequested = true
	p.window.Invalidate()
	if p.serverDialog != nil {
		_ = serverDialogChanged.EmitPage(p.serverDialog.Page(), p.serverDialogSnapshot())
		return
	}
	p.prepareServerDialog()
}

func (p *panel) prepareServerDialog() {
	if p.quitting || p.window == nil || p.serverDialog != nil {
		return
	}
	snapshot, _ := json.Marshal(p.serverDialogSnapshot())
	options := chatWebViewOptions()
	options.Page.PreloadScript += "\nwindow.spoolServerDialog = " + string(snapshot) + `;
window.mygo.on("spool:server-dialog", value => {
  window.spoolServerDialog = value;
  window.dispatchEvent(new CustomEvent("spool:server-dialog", {detail: value}));
});`
	dialog, err := p.window.NewWebView(options)
	if err != nil {
		p.hideServerDialog()
		p.message = err.Error()
		return
	}
	p.serverDialog = dialog
	fail := func(err error) {
		if p.serverDialog != dialog || p.quitting {
			return
		}
		requested := p.serverDialogRequested
		p.hideServerDialog()
		p.serverDialog = nil
		dialog.Destroy()
		if !requested {
			return
		}
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Parent: p.window, Type: mygo.MessageError, Title: p.tr("Settings"), Message: err.Error()})
	}
	dialog.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		if p.serverDialogDocumentAllowed(e.URL) {
			return
		}
		e.PreventDefault()
		if e.UserInitiated {
			p.openChatLink(e.URL)
		}
	})
	dialog.Page().SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions { p.openChatLink(req.URL); return nil })
	dialog.Page().OnDidFailLoad(func(err *mygo.LoadError) {
		fail(err)
	})
	dialog.Page().OnRenderProcessGone(func(reason string) {
		fail(errors.New(reason))
	})
	if err := dialog.Page().LoadURL(p.serverDialogURL()); err != nil {
		fail(err)
	}
}

func (p *panel) hideServerDialog() {
	wasOpen := p.serverDialogOpen || p.serverDialogRequested
	p.serverDialogRequested = false
	p.serverDialogOpen = false
	if p.serverPairing != nil {
		p.serverPairing.Close()
		p.serverPairing = nil
	}
	if p.window != nil {
		p.window.Invalidate()
		if wasOpen && !p.quitting {
			view := p.settingsView
			if p.tab == "Chat" {
				view = p.chatPresentedView
			}
			if view != nil {
				view.Focus()
			}
		}
	}
}

func (p *panel) serverDialogContent(c *ui.Context) {
	if p.serverDialog == nil {
		return
	}
	width, height := c.Size()
	w, h := p.serverDialogSize(width, height)
	c.Key("server-settings-modal")
	modal := ui.Modal(c, &p.serverDialogOpen, func() {
		ui.WebView(c, p.serverDialog).Fill().Radius(16)
	}).Width(w).Height(h).Padding(0).Gap(0).Radius(16)
	if modal.Dismissed() {
		p.hideServerDialog()
	}
}

func (p *panel) serverDialogSize(width, height float32) (float32, float32) {
	contentHeight := p.serverDialogHeight
	if contentHeight <= 0 {
		contentHeight = 660
	}
	return min(560, width-40), min(contentHeight, 660, height-40)
}

func (p *panel) desktopWindowCount() int {
	return len(mygo.Windows())
}

func serverDialogUI[T any](ctx context.Context, p *panel, fn func() (T, error)) (T, error) {
	var zero T
	if p == nil || p.window == nil {
		return zero, errors.New("server settings are unavailable")
	}
	type result struct {
		value T
		err   error
	}
	reply := make(chan result, 1)
	p.window.Update(func() {
		caller := mygo.CallerPage(ctx)
		if ctx.Err() != nil {
			reply <- result{err: ctx.Err()}
			return
		}
		if p.quitting || p.serverDialog == nil || caller == nil || caller != p.serverDialog.Page() || !p.serverDialogDocumentAllowed(caller.URL()) {
			reply <- result{err: errors.New("only the server settings dialog may manage this connection")}
			return
		}
		value, err := fn()
		reply <- result{value, err}
	})
	select {
	case result := <-reply:
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (p *panel) serverDialogSnapshot() desktopServerSnapshot {
	server, ok := p.servers.find(p.serverDialogID)
	if !ok {
		server = desktopServer{Mode: "direct", Automatic: true}
	}
	status := "disconnected"
	active := false
	if chat := p.serverChats[server.ID]; chat != nil && server.Enabled {
		status = chat.status
		active = !chat.failed && (status == "connecting" || status == "connected" || status == "login")
	}
	return desktopServerSnapshot{Server: server, Status: status, Selected: p.selectedDesktopServer() == server.ID, Active: active}
}

func (s *desktopServerSettings) Snapshot(ctx context.Context) (desktopServerSnapshot, error) {
	return serverDialogUI(ctx, s.panel, func() (desktopServerSnapshot, error) { return s.panel.serverDialogSnapshot(), nil })
}

func (s *desktopServerSettings) Ready(ctx context.Context, id string, height int) (bool, error) {
	return serverDialogUI(ctx, s.panel, func() (bool, error) {
		// DOM ready precedes React's initial snapshot and theme. Let the editor
		// reveal its modal once the first usable view has been committed.
		if !s.panel.serverDialogRequested {
			return false, nil
		}
		if id != s.panel.serverDialogID {
			_ = serverDialogChanged.EmitPage(s.panel.serverDialog.Page(), s.panel.serverDialogSnapshot())
			return false, nil
		}
		if height <= 0 {
			return false, errors.New("invalid server settings height")
		}
		if next := float32(min(height, 660)); next != s.panel.serverDialogHeight {
			s.panel.serverDialogHeight = next
			s.panel.window.Invalidate()
		}
		if !s.panel.serverDialogOpen {
			s.panel.serverDialogOpen = true
			s.panel.sendDesktopAppearance(s.panel.serverDialog)
			s.panel.window.Invalidate()
			s.panel.serverDialog.Focus()
		}
		return true, nil
	})
}

func (s *desktopServerSettings) Save(ctx context.Context, server desktopServer, connect bool) (desktopServerSnapshot, error) {
	p := s.panel
	return serverDialogUI(ctx, p, func() (desktopServerSnapshot, error) {
		if server.ID != p.serverDialogID || server.ID == localDesktopServer {
			return desktopServerSnapshot{}, errors.New("invalid server settings target")
		}
		if connect {
			server.Enabled = true
		}
		previous, _ := p.servers.find(server.ID)
		if previous.FederationRelay {
			server.FederationRelay = true
			server.FederationSource, server.FederationTarget = previous.FederationSource, previous.FederationTarget
			server.FederationAccount, server.ConnectionID = previous.FederationAccount, previous.ConnectionID
			server.Direct, server.Tailscale, server.Relay, server.Mode = previous.Direct, previous.Tailscale, previous.Relay, previous.Mode
		}
		next, err := p.servers.save(server)
		if err != nil {
			return desktopServerSnapshot{}, err
		}
		id := server.ID
		if id == "" {
			id = next.Entries[len(next.Entries)-1].ID
		}
		if err = p.setDesktopServers(next); err != nil {
			return desktopServerSnapshot{}, err
		}
		if p.serverPairing != nil && previous.Relay != server.Relay {
			p.serverPairing.Close()
			p.serverPairing = nil
		}
		p.serverDialogID = id
		if connect {
			if chat := p.serverChats[id]; chat != nil && (!previous.Enabled || chat.status == "error" || chat.status == "disconnected") {
				chat.failed = true
			}
			if err = p.selectDesktopServer(id); err != nil {
				return desktopServerSnapshot{}, err
			}
		} else if p.chatView == nil {
			p.openChat()
		}
		return p.serverDialogSnapshot(), nil
	})
}

func (p *panel) disconnectDesktopServer(id string) error {
	server, ok := p.servers.find(id)
	if !ok || id == localDesktopServer {
		return errors.New("invalid server settings target")
	}
	server.Enabled = false
	next, err := p.servers.save(server)
	if err != nil {
		return err
	}
	if err = p.setDesktopServers(next); err != nil {
		return err
	}
	if p.serverDialogID == id && p.serverPairing != nil {
		p.serverPairing.Close()
		p.serverPairing = nil
	}
	if p.window != nil && p.selectedDesktopServer() == id {
		p.openChat()
	}
	return nil
}

func (s *desktopServerSettings) Disconnect(ctx context.Context) (desktopServerSnapshot, error) {
	p := s.panel
	return serverDialogUI(ctx, p, func() (desktopServerSnapshot, error) {
		if err := p.disconnectDesktopServer(p.serverDialogID); err != nil {
			return desktopServerSnapshot{}, err
		}
		return p.serverDialogSnapshot(), nil
	})
}

func (s *desktopServerSettings) Delete(ctx context.Context) (bool, error) {
	p := s.panel
	return serverDialogUI(ctx, p, func() (bool, error) {
		next, err := p.servers.remove(p.serverDialogID)
		if err != nil {
			return false, err
		}
		if err = p.setDesktopServers(next); err != nil {
			return false, err
		}
		p.openChat()
		p.hideServerDialog()
		return true, nil
	})
}

func (s *desktopServerSettings) Close(ctx context.Context) (bool, error) {
	return serverDialogUI(ctx, s.panel, func() (bool, error) { s.panel.hideServerDialog(); return true, nil })
}

func (s *desktopServerSettings) TailscaleLogin(ctx context.Context) (bool, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	server := snapshot.Server
	server.Mode, server.Address = "tailscale", server.Tailscale
	if server.ID == "" || server.Address == "" {
		return false, errors.New("Configure the selected connection before connecting.")
	}
	client, err := s.panel.serverNetworks.client(server)
	if err != nil {
		return false, err
	}
	address, err := client.LoginURL()
	if err != nil {
		return false, err
	}
	return serverDialogUI(ctx, s.panel, func() (bool, error) {
		if !tailnet.IsLoginURL(address) {
			return false, errors.New("Tailscale returned an invalid login link.")
		}
		s.panel.openChatLink(address)
		return true, nil
	})
}

type desktopPairingState struct {
	State       string `json:"state"`
	Fingerprint string `json:"fingerprint"`
}

func (s *desktopServerSettings) PairRelay(ctx context.Context) (desktopPairingState, error) {
	p := s.panel
	type source struct {
		server  desktopServer
		session *spoolnet.PairingSession
	}
	value, err := serverDialogUI(ctx, p, func() (source, error) {
		server, ok := p.servers.find(p.serverDialogID)
		if !p.serverDialogRequested || !ok || !server.Enabled || server.Relay == "" || server.FederationRelay {
			return source{}, errors.New("Configure the selected connection before connecting.")
		}
		return source{server, p.serverPairing}, nil
	})
	if err != nil {
		return desktopPairingState{}, err
	}
	if value.session == nil {
		session, err := spoolnet.BeginRelayPairing(p.serverNetworks.directory(value.server.ID, "relay"), value.server.Relay, "Spool Desktop")
		if err != nil {
			return desktopPairingState{}, err
		}
		_, err = serverDialogUI(ctx, p, func() (bool, error) {
			current, exists := p.servers.find(value.server.ID)
			if !p.serverDialogRequested || p.serverDialogID != value.server.ID || !exists || current != value.server || p.serverPairing != nil {
				return false, fmt.Errorf("pairing was replaced")
			}
			p.serverPairing = session
			return true, nil
		})
		if err != nil {
			session.Close()
			return desktopPairingState{}, err
		}
		value.session = session
	}
	state, err := value.session.Poll()
	if err != nil {
		return desktopPairingState{}, err
	}
	if state == "approved" {
		code, err := value.session.Code()
		if err != nil {
			return desktopPairingState{}, err
		}
		_, err = serverDialogUI(ctx, p, func() (bool, error) {
			server, ok := p.servers.find(value.server.ID)
			if !p.serverDialogRequested || !ok || p.serverPairing != value.session || server.Relay != value.server.Relay {
				return false, errors.New("pairing was replaced")
			}
			server.Relay, server.Mode, server.Enabled = code, "relay", true
			next, err := p.servers.save(server)
			if err != nil {
				return false, err
			}
			if err = p.setDesktopServers(next); err != nil {
				return false, err
			}
			value.session.Close()
			p.serverPairing = nil
			return true, p.selectDesktopServer(server.ID)
		})
		if err != nil {
			return desktopPairingState{}, err
		}
	}
	return desktopPairingState{state, value.session.Fingerprint()}, nil
}
