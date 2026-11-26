package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/egoist/mygo"
)

func (p *panel) smokeRendererOnly(report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	err := p.probeDesktopRenderers(ctx)
	if err == nil {
		body, _ := json.Marshal(map[string]any{
			"scope": "renderer", "native": true, "foreground_recovered": true, "background_recovered": true, "selection_preserved": true,
			"background_recovery_presented": true, "dialog_recovery_presented": true, "preloaded_dialog_recovery_presented": true, "recovery_retry_clears_failure": true,
		})
		err = os.WriteFile(report, body, 0600)
	}
	result <- err
	mygo.App.Quit()
}

func (p *panel) probeDesktopRenderers(ctx context.Context) error {
	ready := make(chan *mygo.WebView, 1)
	p.window.Update(func() { ready <- p.chatView })
	var local *mygo.WebView
	select {
	case local = <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if local == nil {
		return fmt.Errorf("missing foreground renderer")
	}
	if err := waitChatScript(ctx, local.Page(), `return !!document.querySelector('.sidebar .desktop-server-tabs');`); err != nil {
		return err
	}
	if err := p.probeDesktopRendererRecovery(ctx, localDesktopServer, local); err != nil {
		return fmt.Errorf("foreground: %w", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"fixture","server_name":"Background"}`))
		case "/api/me":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Please sign in"}`))
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	type background struct {
		id   string
		view *mygo.WebView
		err  error
	}
	opened := make(chan background, 1)
	p.window.Update(func() {
		next, err := p.servers.save(desktopServer{Direct: upstream.URL, Enabled: true})
		if err != nil {
			opened <- background{err: err}
			return
		}
		id := next.Entries[len(next.Entries)-1].ID
		if err = p.setDesktopServers(next); err == nil {
			server, _ := next.find(id)
			err = p.loadDesktopServerChat(id, p.serverGateway.url+desktopServerMount(server))
		}
		if err != nil {
			opened <- background{err: err}
			return
		}
		opened <- background{id: id, view: p.serverChats[id].view}
	})
	select {
	case state := <-opened:
		if state.err != nil {
			return state.err
		}
		if err := waitChatScript(ctx, state.view.Page(), `return !!document.querySelector('.sidebar .desktop-server-tabs');`); err != nil {
			return err
		}
		if err := p.probeDesktopRendererRecovery(ctx, state.id, state.view); err != nil {
			return fmt.Errorf("background: %w", err)
		}
		if err := p.probeDesktopBackgroundRecoveryScreen(ctx, state.id, state.view, local); err != nil {
			return fmt.Errorf("background recovery screen: %w", err)
		}
		if err := p.probeDesktopDialogRecoveryScreen(ctx, state.id); err != nil {
			return fmt.Errorf("dialog recovery screen: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Browser smoke covers the React boundary. These documents emulate its committed
// fallback without an App that can send Ready, exercising the native navigation
// guard, cached-page selection and modal presentation independently of React.
func loadDesktopRecoveryFixture(ctx context.Context, view *mygo.WebView, address string) error {
	target, _ := json.Marshal(address)
	view.Page().LoadHTML(fmt.Sprintf(`<!doctype html><main class="application-error" role="alert">
<h1>Recovery fixture</h1><button id="retry">Retry</button></main><script>
window.spoolRecoveryFixture = true;
window.spoolReportRecovery = fatal => location.assign('/__desktop/render-error?' +
  new URLSearchParams({name: 'Error', components: 'at RecoveryFixture', fatal: String(fatal)}));
document.querySelector('#retry').onclick = () => location.assign(%s);
</script>`, target), address)
	return waitChatScript(ctx, view.Page(), `return window.spoolRecoveryFixture && !!document.querySelector('.application-error');`)
}

func (p *panel) rendererSmokeUI(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	p.window.Update(func() {
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		done <- fn()
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *panel) probeDesktopBackgroundRecoveryScreen(ctx context.Context, id string, view, foreground *mygo.WebView) error {
	var address string
	if err := p.rendererSmokeUI(ctx, func() error {
		address = p.serverChats[id].address
		return nil
	}); err != nil {
		return err
	}
	if err := loadDesktopRecoveryFixture(ctx, view, address); err != nil {
		return err
	}
	if _, err := view.Page().EvalContext(ctx, `window.spoolReportRecovery(true);`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return p.serverChats[id].renderFailed && p.chatView == foreground && p.chatPresentedView == foreground && p.selectedDesktopServer() == localDesktopServer
	}); err != nil {
		return fmt.Errorf("background failure changed the foreground: %w", err)
	}
	if err := p.rendererSmokeUI(ctx, func() error { return p.selectDesktopServer(id) }); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return p.chatView == view && p.chatPresentedView == view && p.serverChats[id].renderFailed
	}); err != nil {
		return fmt.Errorf("selected recovery screen stayed covered: %w", err)
	}
	// A successful React Retry keeps the document and sends Ready again.
	if _, err := view.Page().EvalContext(ctx, `location.assign('/__desktop/server-ready?' + new URLSearchParams({state: 'login', selection: window.spoolServers.selection, authenticated: 'false'}));`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return !p.serverChats[id].renderFailed }); err != nil {
		return fmt.Errorf("in-document retry retained the failure state: %w", err)
	}
	if _, err := view.Page().EvalContext(ctx, `window.spoolReportRecovery(true);`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverChats[id].renderFailed }); err != nil {
		return err
	}
	if _, err := view.Page().EvalContext(ctx, `document.querySelector('#retry').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, view.Page(), `return !window.spoolRecoveryFixture && !!document.querySelector('.sidebar .desktop-server-tabs');`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return !p.serverChats[id].renderFailed }); err != nil {
		return fmt.Errorf("retry retained the failure state: %w", err)
	}
	if err := p.rendererSmokeUI(ctx, func() error { return p.selectDesktopServer(localDesktopServer) }); err != nil {
		return err
	}
	return p.waitDesktopServerUI(ctx, func() bool { return p.chatView == foreground && p.chatPresentedView == foreground })
}

func (p *panel) probeDesktopDialogRecoveryScreen(ctx context.Context, id string) error {
	var dialog *mygo.WebView
	if err := p.rendererSmokeUI(ctx, func() error {
		p.hideServerDialog()
		dialog = p.serverDialog
		return nil
	}); err != nil {
		return err
	}
	if dialog == nil {
		return fmt.Errorf("missing preloaded server dialog")
	}
	for _, preloaded := range []bool{false, true} {
		if err := loadDesktopRecoveryFixture(ctx, dialog, p.serverDialogURL()); err != nil {
			return err
		}
		if !preloaded {
			if err := p.rendererSmokeUI(ctx, func() error {
				p.showServerDialog(id)
				if p.serverDialogOpen {
					return fmt.Errorf("dialog showed before its content was ready")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if _, err := dialog.Page().EvalContext(ctx, `window.spoolReportRecovery(false);`); err != nil {
			return err
		}
		if err := p.rendererSmokeUI(ctx, func() error {
			if p.serverDialogFailed || p.serverDialogOpen {
				return fmt.Errorf("nonfatal rich text failure revealed a dialog")
			}
			return nil
		}); err != nil {
			return err
		}
		if _, err := dialog.Page().EvalContext(ctx, `window.spoolReportRecovery(true);`); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogFailed }); err != nil {
			return err
		}
		if err := p.rendererSmokeUI(ctx, func() error {
			if preloaded && (p.serverDialogRequested || p.serverDialogOpen) {
				return fmt.Errorf("preloaded failure opened an unsolicited modal")
			}
			p.showServerDialog(id)
			return nil
		}); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogOpen && p.serverDialog == dialog }); err != nil {
			return fmt.Errorf("recovery dialog stayed hidden (preloaded=%t): %w", preloaded, err)
		}
		if err := p.rendererSmokeUI(ctx, func() error {
			p.hideServerDialog()
			p.showServerDialog("")
			if !p.serverDialogOpen || p.serverDialog != dialog {
				return fmt.Errorf("reopening a cached recovery dialog stayed hidden")
			}
			return nil
		}); err != nil {
			return err
		}
		if _, err := dialog.Page().EvalContext(ctx, `return await window.mygo.call('DesktopServers.Ready', '', 320);`); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogOpen && !p.serverDialogFailed }); err != nil {
			return fmt.Errorf("in-document dialog retry retained the failure state: %w", err)
		}
		if _, err := dialog.Page().EvalContext(ctx, `window.spoolReportRecovery(true);`); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogFailed }); err != nil {
			return err
		}
		if _, err := dialog.Page().EvalContext(ctx, `document.querySelector('#retry').click();`); err != nil {
			return err
		}
		if err := waitChatScript(ctx, dialog.Page(), `return !window.spoolRecoveryFixture && !!document.querySelector('.server-dialog-shell');`); err != nil {
			return err
		}
		if err := p.waitDesktopServerUI(ctx, func() bool { return p.serverDialogOpen && !p.serverDialogFailed }); err != nil {
			return fmt.Errorf("dialog retry did not restore readiness: %w", err)
		}
		if err := p.rendererSmokeUI(ctx, func() error { p.hideServerDialog(); return nil }); err != nil {
			return err
		}
	}
	return nil
}

// Inject the production process-gone callback on an isolated smoke page, then
// verify a new document loads without changing the selected server or view.
func (p *panel) probeDesktopRendererRecovery(ctx context.Context, id string, view *mygo.WebView) error {
	// Unlike a real dead process, the injected page can still send navigation
	// hints. Let initial readiness finish so those hints cannot cancel Reload.
	if err := p.waitDesktopServerUI(ctx, func() bool {
		state := p.serverChats[id]
		return state != nil && state.loaded && !state.failed && state.status != "connecting" &&
			(p.chatView != view || p.chatPresentedView == view)
	}); err != nil {
		return err
	}
	if _, err := view.Page().EvalContext(ctx, `window.spoolRendererProbe = true;`); err != nil {
		return err
	}
	dispatched := make(chan error, 1)
	var selected string
	var foreground *mygo.WebView
	p.window.Update(func() {
		state := p.serverChats[id]
		if state == nil || state.view != view {
			dispatched <- fmt.Errorf("renderer probe lost its page")
			return
		}
		selected, foreground = p.selectedDesktopServer(), p.chatView
		p.desktopChatRendererGone(id, state, "smoke-injected")
		dispatched <- nil
	})
	select {
	case err := <-dispatched:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := waitChatScript(ctx, view.Page(), `return !window.spoolRendererProbe && !!document.querySelector('.sidebar .desktop-server-tabs');`); err != nil {
		diagnostic, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		state, diagnosticErr := view.Page().EvalContext(diagnostic, `return JSON.stringify({url: location.href, probe: window.spoolRendererProbe, ready: document.readyState, content: document.getElementById('root')?.textContent.slice(0, 300)});`)
		return fmt.Errorf("renderer did not reload its workspace: %w (page: %v; %v)", err, state, diagnosticErr)
	}
	checked := make(chan bool, 1)
	p.window.Update(func() {
		state := p.serverChats[id]
		checked <- state != nil && state.view == view && state.loaded && !state.failed &&
			p.selectedDesktopServer() == selected && p.chatView == foreground && p.tab == "Chat"
	})
	select {
	case ok := <-checked:
		if !ok {
			return fmt.Errorf("renderer recovery changed selection or left the page failed")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
