package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/i18n"
	"spool/internal/workspace"
)

// The release probe never opens a caller-supplied database, probes an agent,
// checks for updates, or takes the installed desktop's single-instance lock.
func smokeData() (string, error) {
	root, err := os.MkdirTemp("", "spool-release-smoke-")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) { os.RemoveAll(root); return "", err }
	for _, key := range []string{"SPOOL_CODEX_BIN", "SPOOL_CLAUDE_BIN"} {
		if err := os.Setenv(key, filepath.Join(root, "no-agent-executable")); err != nil {
			return fail(err)
		}
	}
	store, err := workspace.OpenStore(root)
	if err != nil {
		return fail(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		store.Close()
		return fail(err)
	}
	settings := workspace.DefaultSettings()
	settings.Port = listener.Addr().(*net.TCPAddr).Port
	// The desktop must load locally even with an HTTPS proxy configured.
	settings.PublicURL = "https://smoke.example.invalid"
	listener.Close()
	err = store.SaveSettings(settings)
	if err == nil {
		err = store.SaveUIPreferences("", workspace.UIPreferences{Theme: "dark", Language: "ja"})
	}
	closeErr := store.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fail(err)
	}
	return root, nil
}

func probeWeb(ctx context.Context, address string) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for _, page := range []struct{ path, contains string }{
		{"/healthz", `"ok":true`}, {"/", "<title>Spool"}, {"/manifest.webmanifest", `"Spool"`},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+page.path, nil)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || !bytes.Contains(data, []byte(page.contains)) {
			return fmt.Errorf("smoke probe failed for %s (HTTP %d): %v", page.path, res.StatusCode, err)
		}
	}
	return nil
}

func (p *panel) smoke(report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Confirm the native event loop still dispatches after window creation.
	dispatched := make(chan struct{})
	p.window.Update(func() { close(dispatched) })
	var err error
	select {
	case <-dispatched:
		for range 3 {
			if err = probeWeb(ctx, p.service.Status().Address); err != nil {
				break
			}
			time.Sleep(time.Second)
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		err = p.probeEmbeddedChat(ctx)
	}
	if err == nil {
		data, _ := json.Marshal(map[string]any{"version": mygo.App.Version(), "target": runtime.GOOS + "-" + runtime.GOARCH, "native": true, "web": true, "chat_webview": true, "single_window": true, "full_window_chat": true, "shared_presentation": true})
		err = os.WriteFile(report, data, 0o600)
	}
	result <- err
	mygo.App.Quit()
}

func (p *panel) probeEmbeddedChat(ctx context.Context) error {
	address, err := p.service.LocalWebURL()
	if err != nil {
		return err
	}
	opened := make(chan *mygo.WebView, 1)
	failure := make(chan error, 1)
	p.window.Update(func() {
		p.selectTab("Chat")
		if err := p.loadChatView(address); err != nil {
			failure <- err
		} else if p.chatView.Window() != p.window || len(mygo.Windows()) != 1 {
			failure <- fmt.Errorf("chat must be embedded in the only main window")
		} else {
			opened <- p.chatView
		}
	})
	var view *mygo.WebView
	select {
	case view = <-opened:
	case err := <-failure:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	page := view.Page()
	if err := waitChatScript(ctx, page, `return window.spoolDesktop === true && window.isSecureContext && !!document.querySelector('input[name="password"]')`); err != nil {
		return err
	}
	if err := p.probeChatViewport(ctx, page); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('input[name="username"]').value = "smoke-draft";`); err != nil {
		return err
	}
	if err := p.probeChatPresentation(ctx, page); err != nil {
		return err
	}
	// Click the real bottom-left settings link. The native navigation guard
	// must cancel it without unloading the page or creating another window.
	if _, err := page.EvalContext(ctx, `document.querySelector('a.desktop-settings').click();`); err != nil {
		return err
	}
	if err := p.waitChatSettings(ctx, view); err != nil {
		return err
	}
	if err := p.smokePresentation(ctx, workspace.UIPreferences{Theme: "light", Language: "zh-CN"}); err != nil {
		return err
	}
	if err := p.probeChatPresentation(ctx, page); err != nil {
		return err
	}
	reopened := make(chan error, 1)
	p.window.Update(func() {
		p.selectTab("Chat")
		if err := p.loadChatView(address); err != nil {
			reopened <- err
			return
		}
		if p.chatView != view || view.Window() != p.window || len(mygo.Windows()) != 1 {
			reopened <- fmt.Errorf("switching back to chat created another page or window")
			return
		}
		p.window.Close()
		if p.window.IsDestroyed() || p.window.IsVisible() || view.IsDestroyed() {
			reopened <- fmt.Errorf("closing the main window did not preserve embedded chat")
			return
		}
		p.showMainWindow()
		reopened <- nil
	})
	select {
	case err = <-reopened:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := waitChatScript(ctx, page, `return document.querySelector('input[name="username"]')?.value === "smoke-draft"`); err != nil {
		return err
	}
	if err := p.probeChatViewport(ctx, page); err != nil {
		return err
	}
	if err := p.smokePresentation(ctx, workspace.DefaultUIPreferences()); err != nil {
		return err
	}
	if err := p.probeChatPresentation(ctx, page); err != nil {
		return err
	}
	// Verify the system engine gets no automatic desktop login. Do not sign in
	// here: an OS engine may share its cookie store with an installed app.
	value, err := page.EvalContext(ctx, `
		const response = await fetch('/api/me');
		const denied = await fetch('/api/preferences', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}'});
		return response.status === 401 && denied.status === 401;
	`)
	if err != nil || value != true {
		return fmt.Errorf("desktop authentication probe failed: %v (%v)", value, err)
	}
	return probeWeb(ctx, p.service.Status().Address)
}

func (p *panel) smokePresentation(ctx context.Context, preferences workspace.UIPreferences) error {
	if err := p.service.Store.SaveUIPreferences("", preferences); err != nil {
		return err
	}
	applied := make(chan struct{})
	p.window.Update(func() {
		p.load()
		p.applyPresentation()
		close(applied)
	})
	select {
	case <-applied:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *panel) probeChatPresentation(ctx context.Context, page *mygo.Page) error {
	theme := "light"
	if mygo.Theme.IsDark() {
		theme = "dark"
	}
	locale := i18n.ResolveLanguage(mygo.App.Locale())
	return waitChatScript(ctx, page, fmt.Sprintf(`
		return document.documentElement.dataset.theme === %q && document.documentElement.lang === %q &&
			document.querySelector('a.desktop-settings')?.getAttribute('aria-label') === %q &&
			!document.querySelector('.login-preferences') && !document.querySelector('.appearance-dialog') &&
			document.querySelector('input[name="username"]')?.value === "smoke-draft";
	`, theme, locale, i18n.Translate(locale, "Settings")))
}

func (p *panel) probeChatViewport(ctx context.Context, page *mygo.Page) error {
	width, height := p.window.ContentSize()
	return waitChatScript(ctx, page, fmt.Sprintf(`
		const link = document.querySelector('a.desktop-settings');
		const bounds = link?.getBoundingClientRect();
		return Math.abs(innerWidth - %d) <= 2 && Math.abs(innerHeight - %d) <= 2 &&
			bounds && bounds.left <= 22 && bounds.bottom >= innerHeight - 22 &&
			bounds.right < innerWidth / 2 && getComputedStyle(link).position === 'fixed';
	`, width, height))
}

func (p *panel) waitChatSettings(ctx context.Context, view *mygo.WebView) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		selected := make(chan bool, 1)
		p.window.Update(func() {
			selected <- p.tab == "Service" && !view.IsDestroyed() && len(mygo.Windows()) == 1
		})
		select {
		case ready := <-selected:
			if ready {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("chat settings link did not select native settings in the main window: %w", ctx.Err())
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("chat settings link did not select native settings in the main window: %w", ctx.Err())
		}
	}
}

func waitChatScript(ctx context.Context, page *mygo.Page, script string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := page.EvalContext(ctx, script)
		if err == nil && value == true {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("desktop chat did not become ready: %w (last result %v, %v)", ctx.Err(), value, err)
		case <-ticker.C:
		}
	}
}
