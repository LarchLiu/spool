package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// This probe uses the disposable smoke service as a remote destination. Browser
// accounts remain signed out, including in OS engines with shared cookie stores.
func (p *panel) smokeServersOnly(report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := p.probeDesktopServers(ctx, report)
	if err == nil {
		body, _ := json.Marshal(map[string]any{"scope": "servers", "target": runtime.GOOS + "-" + runtime.GOARCH, "native": true, "server_tabs": true, "inline_modal": true, "isolated_bridge": true, "preserved_page": true, "ready_before_reveal": true, "reused_editor": true, "disconnected_sidebar": true, "explicit_sign_in": true, "shared_sidebar_width": true, "paint_before_switch": true, "disconnect_reconnect": true, "responsive_startup": true, "background_status": true, "tray_dialog_restore": true})
		err = os.WriteFile(report, body, 0600)
	}
	result <- err
	mygo.App.Quit()
}

func (p *panel) probeDesktopServers(ctx context.Context, report string) (probeErr error) {
	stage := "local sidebar"
	defer func() {
		if probeErr != nil {
			probeErr = fmt.Errorf("desktop servers (%s): %w", stage, probeErr)
		}
	}()
	ready := make(chan *mygo.WebView, 1)
	p.window.Update(func() { ready <- p.chatView })
	var local *mygo.WebView
	select {
	case local = <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if local == nil {
		return fmt.Errorf("missing local chat")
	}
	localCtx, localCancel := context.WithTimeout(ctx, 8*time.Second)
	err := waitChatScript(localCtx, local.Page(), `return document.querySelector('.desktop-server-tabs button')?.textContent.trim() === 'Home' && document.querySelector('.side-foot > .desktop-server-action')?.disabled === false && window.spoolServers.entries.find(server => server.id === 'local')?.status === 'login';`)
	localCancel()
	if err != nil {
		state, _ := local.Page().EvalContext(ctx, `return JSON.stringify({tabs: window.spoolServers, name: document.querySelector('.desktop-server-tabs button')?.textContent.trim(), action: document.querySelector('.side-foot > .desktop-server-action')?.outerHTML, url: location.href, visibility: document.visibilityState, bounds: document.documentElement.getBoundingClientRect().toJSON()});`)
		frames, _ := local.Page().EvalContext(ctx, `return await new Promise(resolve => { setTimeout(() => resolve(false), 300); requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))); });`)
		return fmt.Errorf("local sidebar did not become ready: %w (presentation: %v; frames: %v)", err, state, frames)
	}
	stage = "responsive startup layout"
	if err := p.probeResponsiveDesktopWorkspace(ctx, local, report); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.side-foot > .desktop-server-action').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, local.Page(), `return !!document.querySelector('input[name="username"]') && !document.querySelector('.desktop-server-tabs');`); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `
		const input = document.querySelector('input[name="username"]');
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, 'local-server-draft');
		input.dispatchEvent(new Event('input', {bubbles: true}));
	`); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.desktop-login-header button').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, local.Page(), `return !!document.querySelector('.sidebar .desktop-server-tabs');`); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.panel-resize-handle.left').dispatchEvent(new KeyboardEvent('keydown', {key: 'ArrowRight', bubbles: true}));`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.servers.SidebarWidth == 308 }); err != nil {
		return fmt.Errorf("sidebar resize did not reach desktop preferences: %w", err)
	}
	saved, err := readDesktopServers(p.serversPath)
	if err != nil || saved.SidebarWidth != 308 {
		return fmt.Errorf("shared sidebar width did not persist: %v (%v)", saved.SidebarWidth, err)
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.desktop-server-tabs a').click();`); err != nil {
		return err
	}
	stage = "opening settings modal"
	dialogReady := make(chan *mygo.WebView, 1)
	p.window.Update(func() { dialogReady <- p.serverDialog })
	var dialog *mygo.WebView
	select {
	case dialog = <-dialogReady:
	case <-ctx.Done():
		return ctx.Err()
	}
	if dialog == nil {
		return fmt.Errorf("add server did not open its modal")
	}
	page := dialog.Page()
	if err := waitChatScript(ctx, page, `return !!document.querySelector('.server-connection-tabs') && !!document.querySelector('form input');`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogOpen && p.desktopWindowCount() == 1 }); err != nil {
		return fmt.Errorf("settings did not open inside the main window: %w", err)
	}
	stage = "restoring add-server dialog from tray"
	if err := p.probeServerDialogFromTray(ctx, "Add server"); err != nil {
		return err
	}
	address, err := p.service.LocalWebURL()
	if err != nil {
		return err
	}
	target, _ := url.Parse(address)
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.Host = target.Host
		if p.In.Header.Get("Origin") != "" {
			p.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
	}}
	var healthStatus atomic.Int32
	healthStatus.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(int(healthStatus.Load()))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	profile, _ := json.Marshal(desktopServer{Direct: upstream.URL, Enabled: true, Mode: "direct", Automatic: true})
	stage = "saving remote server"
	if _, err := page.EvalContext(ctx, fmt.Sprintf(`return await mygo.call('DesktopServers.Save', %s, true);`, profile)); err != nil {
		return err
	}
	remoteReady := make(chan *mygo.WebView, 1)
	p.window.Update(func() { remoteReady <- p.chatView })
	var remote *mygo.WebView
	select {
	case remote = <-remoteReady:
	case <-ctx.Done():
		return ctx.Err()
	}
	if remote == nil || remote == local {
		return fmt.Errorf("remote did not get an independent page")
	}
	stage = "remote sidebar and bridge isolation"
	remoteCtx, remoteCancel := context.WithTimeout(ctx, 8*time.Second)
	err = waitChatScript(remoteCtx, remote.Page(), `return document.querySelectorAll('.sidebar .desktop-server-track button').length === 2 && document.querySelector('[aria-current="page"]')?.textContent.trim() === 'Home' && !document.querySelector('input[name="password"]') && window.spoolServers.transition?.from === 'local' && window.spoolServers.transition.started_at > 0 && window.spoolServers.entries.find(server => server.id === window.spoolServers.selected)?.status === 'login';`)
	remoteCancel()
	if err != nil {
		state, _ := remote.Page().EvalContext(ctx, `return JSON.stringify({tabs: window.spoolServers, status: document.querySelector('.desktop-server-welcome [role="status"]')?.textContent, error: document.querySelector('[role="alert"]')?.textContent, requests: await Promise.all(['version', 'me'].map(async name => { const response = await fetch(location.pathname + 'api/' + name, {cache: 'no-store'}); return {name, status: response.status, text: (await response.text()).slice(0, 200)}; }))});`)
		return fmt.Errorf("remote sidebar was not ready: %w (state: %v)", err, state)
	}
	denied, err := remote.Page().EvalContext(ctx, `
		let rejected = 0;
		for (const name of ['DesktopServers.Snapshot', 'DesktopServers.Ready', 'DesktopServers.Disconnect', 'DesktopSettings.Snapshot']) {
			try { await mygo.call(name); } catch { rejected++; }
		}
		return rejected === 4 && (await fetch(location.pathname + 'api/me')).status === 401;
	`)
	if err != nil || denied != true {
		return fmt.Errorf("remote bridge/account isolation: %v (%v)", denied, err)
	}
	if _, err := page.EvalContext(ctx, `const data = await mygo.call('DesktopServers.Snapshot'); data.server.name = '我的远程'; return await mygo.call('DesktopServers.Save', data.server, false);`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, remote.Page(), `return document.querySelector('[aria-current="page"]')?.textContent.trim() === '我的远程';`); err != nil {
		return err
	}
	stage = "disconnecting remote server"
	disconnected, err := page.EvalContext(ctx, `
		const before = await mygo.call('DesktopServers.Snapshot');
		const after = await mygo.call('DesktopServers.Disconnect');
		return before.active && !after.active && after.status === 'disconnected' &&
			!after.server.enabled && after.selected && after.server.direct === before.server.direct &&
			after.server.name === before.server.name && after.server.mode === before.server.mode;
	`)
	if err != nil || disconnected != true {
		return fmt.Errorf("disconnect did not preserve the selected server settings: %v (%v)", disconnected, err)
	}
	type offlineState struct {
		view  *mygo.WebView
		label string
	}
	offlineReady := make(chan offlineState, 1)
	p.window.Update(func() { offlineReady <- offlineState{p.chatView, p.tr("Connect")} })
	var offline *mygo.WebView
	var connectLabel string
	select {
	case state := <-offlineReady:
		offline, connectLabel = state.view, state.label
	case <-ctx.Done():
		return ctx.Err()
	}
	if offline == nil {
		return fmt.Errorf("disconnect lost the selected sidebar")
	}
	offlineCtx, offlineCancel := context.WithTimeout(ctx, 6*time.Second)
	err = waitChatScript(offlineCtx, offline.Page(), fmt.Sprintf(`return !!document.querySelector('.desktop-server-start .sidebar') && document.querySelector('.side-foot > .desktop-server-action')?.getAttribute('aria-label') === %q;`, connectLabel))
	offlineCancel()
	if err != nil {
		state, _ := offline.Page().EvalContext(ctx, `return JSON.stringify({url: location.href, ready: document.readyState, tabs: window.spoolServers, shell: !!document.querySelector('.desktop-server-start .sidebar'), footer: document.querySelector('.side-foot')?.textContent, actions: [...document.querySelectorAll('.side-foot button')].map(button => ({label: button.getAttribute('aria-label'), text: button.textContent}))});`)
		return fmt.Errorf("disconnected sidebar was not ready: %w (state: %v)", err, state)
	}
	stage = "reconnecting remote server"
	if _, err := page.EvalContext(ctx, `const data = await mygo.call('DesktopServers.Snapshot'); return await mygo.call('DesktopServers.Save', data.server, true);`); err != nil {
		return err
	}
	reconnected := make(chan *mygo.WebView, 1)
	p.window.Update(func() { reconnected <- p.chatView })
	select {
	case remote = <-reconnected:
	case <-ctx.Done():
		return ctx.Err()
	}
	if remote == nil {
		return fmt.Errorf("reconnect did not restore the remote view")
	}
	reconnectCtx, reconnectCancel := context.WithTimeout(ctx, 6*time.Second)
	err = waitChatScript(reconnectCtx, remote.Page(), `return window.spoolServers.entries.find(server => server.id === window.spoolServers.selected)?.status === 'login';`)
	reconnectCancel()
	if err != nil {
		state, _ := remote.Page().EvalContext(ctx, `return JSON.stringify({url: location.href, ready: document.readyState, tabs: window.spoolServers, status: document.querySelector('.desktop-server-welcome [role="status"]')?.textContent, footer: document.querySelector('.side-foot')?.textContent, error: document.querySelector('[role="alert"]')?.textContent, requests: await Promise.all(['version', 'me'].map(async name => { const response = await fetch(location.pathname + 'api/' + name, {cache: 'no-store'}); return {name, status: response.status, text: (await response.text()).slice(0, 200)}; }))});`)
		return fmt.Errorf("reconnected sidebar was not ready: %w (state: %v)", err, state)
	}
	stage = "capturing settings modal"
	if image, err := dialog.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-dialog.png", image, 0600); err != nil {
		return err
	}
	// Close on the UI dispatcher, then exercise the actual server navigation hint.
	stage = "closing settings modal"
	closed := make(chan struct{})
	p.window.Update(func() { p.hideServerDialog(); close(closed) })
	select {
	case <-closed:
	case <-ctx.Done():
		return ctx.Err()
	}
	presentedCtx, presentedCancel := context.WithTimeout(ctx, 5*time.Second)
	err = p.waitDesktopServerUI(presentedCtx, func() bool { return p.chatPresentedView == remote })
	presentedCancel()
	if err != nil {
		state, _ := remote.Page().EvalContext(ctx, `return JSON.stringify({readyState: document.readyState, login: !!document.querySelector('.desktop-login'), sidebar: !!document.querySelector('.sidebar'), selected: window.spoolServers?.selected, transition: window.spoolServers?.transition, tabs: document.querySelectorAll('.desktop-server-tabs button').length});`)
		frames, _ := remote.Page().EvalContext(ctx, `return await new Promise(resolve => { setTimeout(() => resolve(false), 300); requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))); });`)
		return fmt.Errorf("remote page was not presented: %w (page: %v; animation frames: %v)", err, state, frames)
	}
	stage = "restoring remote settings from tray"
	if err := p.probeServerDialogFromTray(ctx, "Settings…"); err != nil {
		return err
	}
	p.window.Update(p.hideServerDialog)
	if err := p.waitDesktopServerUI(ctx, func() bool { return !p.serverDialogRequested }); err != nil {
		return err
	}
	// Hold the cached target's paint notifications to observe the outgoing page.
	stage = "staging cached server"
	if _, err := local.Page().EvalContext(ctx, `
		window.spoolSmokeFrames = [];
		window.spoolSmokeRAF = window.requestAnimationFrame;
		window.spoolSmokeCancelRAF = window.cancelAnimationFrame;
		window.requestAnimationFrame = fn => window.spoolSmokeFrames.push(fn);
		window.cancelAnimationFrame = () => {};
	`); err != nil {
		return err
	}
	if _, err := remote.Page().EvalContext(ctx, `document.querySelector('.desktop-server-track button').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, local.Page(), `return window.spoolSmokeFrames.length > 0;`); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `return await new Promise(resolve => {
		const receive = event => {
			if (event.detail?.selection !== 'stale') return;
			window.removeEventListener('spool:server-ready-ack', receive);
			resolve(true);
		};
		window.addEventListener('spool:server-ready-ack', receive);
		location.assign('/__desktop/server-ready?state=login&selection=stale');
	});`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.chatView == local && p.chatPresentedView == remote }); err != nil {
		return fmt.Errorf("server switch did not retain the outgoing page until paint: %w", err)
	}
	if _, err := local.Page().EvalContext(ctx, `
		const frames = window.spoolSmokeFrames;
		window.requestAnimationFrame = window.spoolSmokeRAF;
		window.cancelAnimationFrame = window.spoolSmokeCancelRAF;
		for (const frame of frames) window.requestAnimationFrame(frame);
	`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.chatPresentedView == local }); err != nil {
		return fmt.Errorf("painted target was not presented: %w", err)
	}
	stage = "background connection status"
	if err := p.probeBackgroundDesktopServerState(ctx, local, remote); err != nil {
		return err
	}
	stage = "background availability and recovery"
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		healthStatus.Store(int32(status))
		p.window.Update(func() {
			for id, chat := range p.serverChats {
				if id != localDesktopServer {
					chat.lastHealthCheck = time.Time{}
				}
			}
			p.checkDesktopServerHealth()
		})
		want := "error"
		if status == http.StatusOK {
			want = "login"
		}
		if err := waitChatScript(ctx, local.Page(), fmt.Sprintf(`return window.spoolServers.entries.find(server => !server.local)?.status === %q;`, want)); err != nil {
			return fmt.Errorf("background HTTP %d did not update the foreground tabs: %w", status, err)
		}
		if err := p.waitDesktopServerUI(ctx, func() bool {
			return p.chatView == local && p.chatPresentedView == local && p.serverDialogSnapshot().Status == want
		}); err != nil {
			return fmt.Errorf("background availability changed selection or did not update settings: %w", err)
		}
	}
	stage = "restoring local login draft"
	if err := waitChatScript(ctx, local.Page(), `return !!document.querySelector('.desktop-server-start') && document.querySelector('[aria-current="page"]')?.textContent.trim() === 'Home';`); err != nil {
		return err
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.side-foot > .desktop-server-action').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, local.Page(), `return document.querySelector('input[name="username"]')?.value === 'local-server-draft' && document.querySelector('.desktop-login-header > span')?.textContent === 'Home' && !document.querySelector('.desktop-server-tabs');`); err != nil {
		return err
	}
	restored := make(chan bool, 1)
	p.window.Update(func() {
		restored <- p.chatView == local && p.selectedDesktopServer() == localDesktopServer && p.desktopWindowCount() == 1
	})
	select {
	case ok := <-restored:
		if !ok {
			return fmt.Errorf("switching servers replaced the local page")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := p.probeServerDialogReveal(ctx, report); err != nil {
		return err
	}
	stage = "disconnected sidebars"
	if err := p.probeDisconnectedDesktopServers(ctx, local); err != nil {
		return err
	}
	stage = "independent remote desktop windows"
	return p.probeRemoteDesktopWindows(ctx)
}

func (p *panel) probeBackgroundDesktopServerState(ctx context.Context, foreground, background *mygo.WebView) error {
	for _, status := range []string{"error", "login"} {
		// The cached document retains its old selection token. Its health must
		// still reach the foreground tabs and settings without presenting it.
		if _, err := background.Page().EvalContext(ctx, fmt.Sprintf(`return await new Promise(resolve => {
			const state = %q;
			const selection = window.spoolServers.selection;
			const receive = event => {
				if (event.detail?.state !== state || event.detail?.selection !== selection) return;
				window.removeEventListener('spool:server-ready-ack', receive);
				resolve(true);
			};
			window.addEventListener('spool:server-ready-ack', receive);
			location.assign('/__desktop/server-ready?' + new URLSearchParams({state, selection}));
		});`, status)); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool {
			return p.chatView == foreground && p.chatPresentedView == foreground && p.serverDialogSnapshot().Status == status
		}); err != nil {
			return fmt.Errorf("background %s did not update settings or changed the presented page: %w", status, err)
		}
		if err := waitChatScript(ctx, foreground.Page(), fmt.Sprintf(`return window.spoolServers.entries.find(server => !server.local)?.status === %q;`, status)); err != nil {
			return fmt.Errorf("background %s did not update the foreground tabs: %w", status, err)
		}
	}
	return nil
}

func (p *panel) probeServerDialogFromTray(ctx context.Context, label string) error {
	p.window.Update(p.hideServerDialog)
	for _, state := range []string{"hidden", "hidden with dialog open", "minimized"} {
		p.window.Update(func() {
			if state == "minimized" {
				p.window.Minimize()
			} else {
				p.window.Hide()
				mygo.App.Dock.Hide()
			}
		})
		if err := p.waitDesktopServerUI(ctx, func() bool {
			if state == "minimized" {
				return p.window.IsMinimized()
			}
			return !p.window.IsVisible()
		}); err != nil {
			return fmt.Errorf("could not prepare %s window: %w", state, err)
		}
		invoked := make(chan bool, 1)
		p.window.Update(func() {
			for _, item := range p.trayMenu().Items() {
				if item.Label == p.tr(label) {
					item.Click(item, p.window)
					invoked <- true
					return
				}
			}
			invoked <- false
		})
		select {
		case found := <-invoked:
			if !found {
				return fmt.Errorf("missing tray action %s", label)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := p.waitDesktopServerUI(ctx, func() bool {
			id := ""
			if label == "Settings…" {
				id = p.selectedDesktopServer()
			}
			return p.window.IsVisible() && !p.window.IsMinimized() && p.serverDialogOpen && p.serverDialogID == id
		}); err != nil {
			return fmt.Errorf("tray %s did not restore %s window and dialog: %w", label, state, err)
		}
	}
	return nil
}

func (p *panel) probeResponsiveDesktopWorkspace(ctx context.Context, view *mygo.WebView, report string) error {
	const wide = `
		const sidebar = document.querySelector('.sidebar');
		const workspace = document.querySelector('.workspace');
		return innerWidth > 900 && !!sidebar && !sidebar.inert && !sidebar.classList.contains('open') &&
			!!sidebar.querySelector('.sidebar-collapse svg.lucide-panel-left-close') &&
			!workspace.querySelector('.panel-toggle-left') &&
			Math.abs(sidebar.getBoundingClientRect().right - workspace.getBoundingClientRect().left) < 1;
	`
	if err := waitChatScript(ctx, view.Page(), wide); err != nil {
		return fmt.Errorf("wide desktop started with a narrow sidebar: %w", err)
	}
	p.window.Update(func() { p.window.SetSize(840, 700) })
	if err := waitChatScript(ctx, view.Page(), `return innerWidth <= 900 && document.querySelector('.sidebar')?.inert && !!document.querySelector('.panel-toggle-left svg.lucide-menu');`); err != nil {
		return fmt.Errorf("narrow desktop did not switch to a drawer: %w", err)
	}
	if _, err := view.Page().EvalContext(ctx, `document.querySelector('.panel-toggle-left').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, view.Page(), `const sidebar = document.querySelector('.sidebar'); return !sidebar.inert && sidebar.classList.contains('open') && !!sidebar.querySelector('.sidebar-collapse svg.lucide-x') && Math.abs(sidebar.getBoundingClientRect().width - innerWidth) < 1;`); err != nil {
		return err
	}
	if _, err := view.Page().EvalContext(ctx, `document.querySelector('.sidebar-collapse').click();`); err != nil {
		return err
	}
	p.window.Update(func() { p.window.SetSize(1280, 860) })
	if err := waitChatScript(ctx, view.Page(), wide); err != nil {
		return fmt.Errorf("wide desktop retained its drawer after resizing: %w", err)
	}
	image, err := view.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(report+"-layout.png", image, 0600); err != nil {
		return err
	}
	return nil
}

func (p *panel) probeDisconnectedDesktopServers(ctx context.Context, local *mygo.WebView) error {
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Could not connect to this server."}`))
	}))
	defer unavailable.Close()
	added := make(chan error, 1)
	p.window.Update(func() {
		next := p.servers
		for _, enabled := range []bool{true, false} {
			name := "Offline server"
			if !enabled {
				name = "Disabled server"
			}
			var err error
			next, err = next.save(desktopServer{Name: name, Direct: unavailable.URL, Enabled: enabled})
			if err != nil {
				added <- err
				return
			}
		}
		added <- p.setDesktopServers(next)
	})
	select {
	case err := <-added:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := local.Page().EvalContext(ctx, `document.querySelector('.desktop-login-header button').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, local.Page(), `return document.querySelectorAll('.desktop-server-track button').length === 4;`); err != nil {
		return err
	}
	page := local.Page()
	for _, name := range []string{"Offline server", "Home", "Disabled server", "Home"} {
		before, err := page.EvalContext(ctx, `const track = document.querySelector('.desktop-server-track'); track.dispatchEvent(new WheelEvent('wheel', {bubbles: true})); track.scrollLeft = track.scrollWidth; return track.scrollLeft;`)
		if err != nil {
			return err
		}
		beforeScroll, ok := before.(float64)
		if !ok {
			return fmt.Errorf("missing outgoing tab position: %v", before)
		}
		encoded, _ := json.Marshal(name)
		target, err := page.EvalContext(ctx, fmt.Sprintf(`Array.from(document.querySelectorAll('.desktop-server-track button')).find(button => button.textContent.trim() === %s).click(); return window.spoolServers.entries.find(server => server.name === %s).id;`, encoded, encoded))
		if err != nil {
			return err
		}
		targetID, ok := target.(string)
		if !ok || targetID == "" {
			return fmt.Errorf("missing tab for %s", name)
		}
		// Navigation hints are asynchronous. Wait for native selection before
		// choosing the page to inspect, rather than accidentally polling the
		// outgoing server's cached page for the incoming server's content.
		selectionCtx, selectionCancel := context.WithTimeout(ctx, 5*time.Second)
		err = p.waitDesktopServerUI(selectionCtx, func() bool { return p.selectedDesktopServer() == targetID && p.chatView != nil })
		selectionCancel()
		if err != nil {
			state, _ := page.EvalContext(ctx, `return JSON.stringify({url: location.href, tabs: window.spoolServers, current: document.querySelector('[aria-current="page"]')?.textContent, status: document.querySelector('.desktop-server-welcome [role="status"]')?.textContent});`)
			return fmt.Errorf("selecting %s: %w (outgoing page: %v)", name, err, state)
		}
		selected := make(chan *mygo.Page, 1)
		p.window.Update(func() { selected <- p.chatView.Page() })
		select {
		case page = <-selected:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return !!document.querySelector('.sidebar .desktop-server-tabs') && document.querySelector('[aria-current="page"]')?.textContent.trim() === %s && document.querySelector('.desktop-server-welcome h1')?.textContent === %s && document.querySelector('.side-foot > .desktop-server-action')?.disabled === false && !document.querySelector('.desktop-login') && Math.round(document.querySelector('.sidebar').getBoundingClientRect().width) === 308;`, encoded, encoded)); err != nil {
			state, _ := page.Eval(`return JSON.stringify({selected: document.querySelector('[aria-current="page"]')?.textContent.trim(), welcome: document.querySelector('.desktop-server-welcome h1')?.textContent, actionDisabled: document.querySelector('.side-foot > .desktop-server-action')?.disabled, login: !!document.querySelector('.desktop-login'), sidebarWidth: document.querySelector('.sidebar')?.getBoundingClientRect().width, tabs: window.spoolServers});`)
			return fmt.Errorf("selecting %s: %w (presentation: %v)", name, err, state)
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.chatPresentedView == p.chatView && !p.serverDialogOpen }); err != nil {
			return err
		}
		scrollCtx, scrollCancel := context.WithTimeout(ctx, 2*time.Second)
		err = waitChatScript(scrollCtx, page, fmt.Sprintf(`return Math.abs(window.spoolServers.transition?.scroll_left - %f) < 1 && document.querySelector('[aria-current="page"]')?.textContent.trim() === %s;`, beforeScroll, encoded))
		scrollCancel()
		if err != nil {
			state, _ := page.EvalContext(ctx, `return JSON.stringify({transition: window.spoolServers.transition, selected: window.spoolServers.selected, scrollLeft: document.querySelector('.desktop-server-track')?.scrollLeft});`)
			return fmt.Errorf("tab scroll position was not handed over: %w (presentation: %v)", err, state)
		}
		if name == "Disabled server" {
			if err := waitChatScript(ctx, page, `const track = document.querySelector('.desktop-server-track').getBoundingClientRect(); const add = document.querySelector('.desktop-server-add').getBoundingClientRect(); return add.left >= track.left && add.right <= track.right + 1;`); err != nil {
				return fmt.Errorf("last server did not reveal add: %w", err)
			}
		}
		windows := make(chan int, 1)
		p.window.Update(func() { windows <- p.desktopWindowCount() })
		select {
		case count := <-windows:
			if count != 1 {
				return fmt.Errorf("selecting disconnected server opened settings instead of its sidebar")
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *panel) probeServerDialogReveal(ctx context.Context, report string) error {
	type initial struct {
		dialog *mygo.WebView
		hidden bool
		reused bool
	}
	created := make(chan initial, 1)
	started := time.Now()
	p.window.Update(func() {
		previous := p.serverDialog
		p.showServerDialog("")
		dialog := p.serverDialog
		if dialog == nil {
			created <- initial{}
			return
		}
		hidden := !p.serverDialogOpen
		p.showServerDialog("")
		created <- initial{dialog, hidden && !p.serverDialogOpen, previous == dialog}
	})
	var state initial
	select {
	case state = <-created:
	case <-ctx.Done():
		return ctx.Err()
	}
	if state.dialog == nil || !state.hidden {
		return fmt.Errorf("server settings showed before the editor was ready")
	}
	if !state.reused {
		return fmt.Errorf("server settings discarded its preloaded editor")
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogOpen && p.desktopWindowCount() == 1 }); err != nil {
		return fmt.Errorf("server settings stayed hidden: %w", err)
	}
	log.Printf("event=smoke_server_modal_revealed reused=%t duration_ms=%d", state.reused, time.Since(started).Milliseconds())
	usable, err := state.dialog.Page().EvalContext(ctx, `
		const input = document.querySelector('form input');
		return !!input && input.getBoundingClientRect().width > 0 &&
			input.value === '' && !document.querySelector('.server-dialog-footer .danger') &&
			['light', 'dark'].includes(document.documentElement.dataset.theme);
	`)
	if err != nil || usable != true {
		return fmt.Errorf("first visible server settings lacked its form/theme: %v (%v)", usable, err)
	}
	if image, err := state.dialog.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-reveal.png", image, 0600); err != nil {
		return err
	}
	closed := make(chan struct{})
	if _, err := state.dialog.Page().EvalContext(ctx, `window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'}));`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return !p.serverDialogOpen && !p.serverDialogRequested }); err != nil {
		return fmt.Errorf("Escape did not dismiss settings: %w", err)
	}
	p.window.Update(func() { close(closed) })
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Read native presentation state only on the UI dispatcher.
func (p *panel) waitDesktopServerUI(ctx context.Context, check func() bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := make(chan bool, 1)
		p.window.Update(func() { state <- check() })
		select {
		case ready := <-state:
			if ready {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
