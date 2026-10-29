package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/appupdate"
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
		{"/healthz", `"ok":true`}, {"/", "<title>Spool"}, {"/manifest.webmanifest", `"Spool"`}, {"/api/version", `"version":`},
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

func loadSmokeReleaseHistory(path string) (*appupdate.ReleaseHistory, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var history appupdate.ReleaseHistory
	if err := json.NewDecoder(io.LimitReader(file, 8<<20)).Decode(&history); err != nil {
		return nil, err
	}
	if history.CurrentVersion == "" || history.TargetVersion == "" || len(history.Releases) < 2 {
		return nil, fmt.Errorf("smoke release history needs a current version, target version and at least two releases")
	}
	return &history, nil
}

func (p *panel) smoke(report string, result chan<- error, history *appupdate.ReleaseHistory) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
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
		if err = p.probeStartupChat(ctx, report); err != nil {
			err = fmt.Errorf("startup chat: %w", err)
		}
	}
	if err == nil {
		if err = p.probeSettings(ctx, report, history); err != nil {
			err = fmt.Errorf("settings: %w", err)
		}
	}
	if err == nil {
		if err = p.probeEmbeddedChat(ctx); err != nil {
			err = fmt.Errorf("embedded chat: %w", err)
		}
	}
	if err == nil {
		data, _ := json.Marshal(map[string]any{"version": mygo.App.Version(), "target": runtime.GOOS + "-" + runtime.GOARCH, "native": true, "web": true, "chat_webview": true, "single_window": true, "full_window_chat": true, "shared_presentation": true, "settings_webview": true, "management_bridge": true})
		err = os.WriteFile(report, data, 0o600)
	}
	result <- err
	mygo.App.Quit()
}

func (p *panel) probeWindowChrome(ctx context.Context, view *mygo.WebView) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	width, height := p.window.Size()
	contentWidth, contentHeight := p.window.ContentSize()
	if width != contentWidth || height != contentHeight {
		return fmt.Errorf("macOS must not reserve a separate system title bar: window %dx%d, content %dx%d", width, height, contentWidth, contentHeight)
	}
	probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := waitChatScript(probeContext, view.Page(), `
		const brand = document.querySelector('.desktop-window-brand');
		const logo = brand?.querySelector('img');
		const left = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--desktop-controls-left'));
		const rect = logo?.getBoundingClientRect();
		const storyBrand = document.querySelector('.desktop-login .login-story > .brand');
		if (storyBrand) {
			const storyLogo = storyBrand.querySelector('img');
			return document.documentElement.dataset.desktopPlatform === 'darwin' &&
				getComputedStyle(brand).display === 'none' && storyBrand.getBoundingClientRect().top >= 70 &&
				storyLogo.complete && storyLogo.naturalWidth > 0 && storyBrand.textContent.includes('Spool');
		}
		return document.documentElement.dataset.desktopPlatform === 'darwin' && left > 60 && left < 100 &&
			logo?.complete && logo.naturalWidth > 0 && rect.width === 28 && rect.top === 13 && rect.left === left + 12 &&
			getComputedStyle(brand).getPropertyValue('--app-region').trim() === 'drag' && !brand.textContent.trim();
	`)
	if err != nil {
		detail, _ := view.Page().EvalContext(ctx, `const brand = document.querySelector('.desktop-window-brand'); const logo = brand?.querySelector('img'); return {platform: document.documentElement.dataset.desktopPlatform, inset: getComputedStyle(document.documentElement).getPropertyValue('--desktop-controls-left'), logo: logo?.getBoundingClientRect().toJSON(), loaded: logo?.complete, naturalWidth: logo?.naturalWidth, drag: brand && getComputedStyle(brand).getPropertyValue('--app-region')};`)
		return fmt.Errorf("macOS window chrome: %w; layout: %v", err, detail)
	}
	return nil
}

func (p *panel) probeStartupChat(ctx context.Context, report string) error {
	ready := make(chan *mygo.WebView, 1)
	p.window.Update(func() {
		if p.tab != "Chat" || !p.service.Status().Running || len(mygo.Windows()) != 1 {
			ready <- nil
			return
		}
		ready <- p.chatView
	})
	var view *mygo.WebView
	select {
	case view = <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if view == nil {
		return fmt.Errorf("desktop launch must show embedded chat with the background service running")
	}
	if err := waitChatScript(ctx, view.Page(), `return window.spoolDesktop === true && !!document.querySelector('input[name="password"]') && !!document.querySelector('a.desktop-settings');`); err != nil {
		return err
	}
	if err := p.probeChatViewport(ctx, view.Page()); err != nil {
		return err
	}
	if err := p.probeWindowChrome(ctx, view); err != nil {
		return err
	}
	if image, err := view.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-startup-chat.png", image, 0o600); err != nil {
		return err
	}
	if err := p.probeLoginAppearance(ctx, view, report); err != nil {
		return err
	}
	if _, err := view.Page().EvalContext(ctx, `document.querySelector('a.desktop-settings').click();`); err != nil {
		return err
	}
	return p.waitChatSettings(ctx, view)
}

func (p *panel) probeLoginAppearance(ctx context.Context, view *mygo.WebView, report string) error {
	preferences, err := p.service.Store.UIPreferences("")
	if err != nil {
		return err
	}
	for _, theme := range []string{"light", "dark"} {
		if err := p.smokePresentation(ctx, workspace.UIPreferences{Theme: theme, Language: "zh-CN"}); err != nil {
			return err
		}
		if err := waitChatChecks(ctx, view.Page(), fmt.Sprintf(`
			const copy = document.querySelector('.login-story-copy');
			const story = document.querySelector('.login-story');
			const input = document.querySelector('.login-panel input');
			const copyRect = copy.getBoundingClientRect(), storyRect = story.getBoundingClientRect();
			const inputRect = input.getBoundingClientRect();
			const footerRect = document.querySelector('.login-foot').getBoundingClientRect();
			const copyOffset = copyRect.left - storyRect.left - parseFloat(getComputedStyle(story).paddingLeft);
			const centerOffset = copyRect.top + copyRect.height / 2 - storyRect.top - storyRect.height / 2;
			const footerOffset = innerHeight - footerRect.bottom - parseFloat(getComputedStyle(story).paddingLeft);
			const checks = {
				theme: document.documentElement.dataset.theme === %q,
				language: document.documentElement.lang === 'zh-CN',
				textAlign: getComputedStyle(copy).textAlign === 'start',
				leftInset: Math.abs(copyOffset) <= 1,
				verticalCenter: Math.abs(centerOffset) <= 35,
				inputSize: inputRect.height >= 40 && inputRect.height <= 44 && getComputedStyle(input).fontSize === '14px',
				footerInset: Math.abs(footerOffset) <= 1,
				noOverflow: document.documentElement.scrollWidth <= innerWidth
			};
			return {ready: Object.values(checks).every(Boolean), checks,
				measurements: {theme: document.documentElement.dataset.theme, language: document.documentElement.lang,
				textAlign: getComputedStyle(copy).textAlign, copyOffset, centerOffset, footerOffset,
				inputHeight: inputRect.height, inputFontSize: getComputedStyle(input).fontSize,
				viewportWidth: innerWidth, scrollWidth: document.documentElement.scrollWidth}};
		`, theme)); err != nil {
			return fmt.Errorf("login appearance %s: %w", theme, err)
		}
		if err := p.probeWindowChrome(ctx, view); err != nil {
			return err
		}
		if image, err := view.CapturePage(); err != nil {
			return err
		} else if err := os.WriteFile(report+"-login-"+theme+".png", image, 0o600); err != nil {
			return err
		}
	}
	return p.smokePresentation(ctx, preferences)
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
	// Click the real login settings link. The native navigation guard
	// must cancel it without unloading the page or creating another window.
	if _, err := page.EvalContext(ctx, `document.querySelector('a.desktop-settings').click();`); err != nil {
		return err
	}
	if err := p.waitChatSettings(ctx, view); err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, p.settingsView.Page(), settingsCommand{Kind: "save-appearance", Preferences: workspace.UIPreferences{Theme: "light", Language: "zh-CN"}}); err != nil {
		return err
	}
	if err := p.probeChatPresentation(ctx, page); err != nil {
		return err
	}
	if _, err := p.settingsView.Page().EvalContext(ctx, `document.querySelector('.settings-sidebar-bottom button.icon').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, p.settingsView.Page(), `return (await mygo.call('DesktopSettings.Snapshot')).section === 'Chat';`); err != nil {
		return fmt.Errorf("settings footer did not return to chat: %w", err)
	}
	reopened := make(chan error, 1)
	p.window.Update(func() {
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
		let nativeDenied = !window.mygo?.call;
		if (!nativeDenied) {
			try { await window.mygo.call('DesktopSettings.Execute', {kind: 'stop-service'}); }
			catch { nativeDenied = true; }
		}
		return response.status === 401 && denied.status === 401 && nativeDenied;
	`)
	if err != nil || value != true {
		return fmt.Errorf("desktop authentication probe failed: %v (%v)", value, err)
	}
	return probeWeb(ctx, p.service.Status().Address)
}

func smokeSettingsCommand(ctx context.Context, page *mygo.Page, command settingsCommand) error {
	body, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = page.EvalContext(ctx, fmt.Sprintf(`return await window.mygo.call('DesktopSettings.Execute', %s);`, body))
	return err
}

func (p *panel) probeSettings(ctx context.Context, report string, history *appupdate.ReleaseHistory) error {
	ready := make(chan *mygo.WebView, 1)
	p.window.Update(func() { ready <- p.settingsView })
	var view *mygo.WebView
	select {
	case view = <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if view == nil {
		return fmt.Errorf("settings WebView was not created")
	}
	page := view.Page()
	if err := waitChatScript(ctx, page, `return !!document.querySelector('[data-page="Service"] input') && !document.querySelector('.settings-content')?.disabled && !document.querySelector('.settings-brand, .settings-breadcrumb, .settings-search, .settings-version') && !!document.querySelector('.settings-sidebar-bottom button.icon') && !!document.querySelector('.settings-sidebar-status .settings-status-dot')`); err != nil {
		return fmt.Errorf("settings render: %w", err)
	}
	// This exercises the real native bridge, not an HTTP management endpoint.
	for _, kind := range []string{"stop-service", "start-service"} {
		if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: kind}); err != nil {
			return err
		}
		wantRunning := kind == "start-service"
		if p.service.Status().Running != wantRunning {
			return fmt.Errorf("settings %s did not change service state", kind)
		}
		if err := waitChatScript(ctx, page, `return !!document.querySelector('[data-section="Service"]') && (await mygo.call('DesktopSettings.Snapshot')).settings.port > 0`); err != nil {
			return err
		}
	}
	// Native navigation events and all seven pages must work in one window.
	for _, section := range []string{"People", "Projects", "Agents", "Online", "Appearance", "About", "Service"} {
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-section="%s"]').click();`, section)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.querySelector('[data-section="%s"]')?.getAttribute('aria-current') === 'page' && !document.querySelector('[data-page="%s"]').hidden && !document.querySelector('.settings-content').disabled`, section, section)); err != nil {
			return err
		}
		if section == "People" || section == "Projects" || section == "Agents" {
			if err := waitChatScript(ctx, page, fmt.Sprintf(`return !!document.querySelector('[data-page="%s"] .settings-list') && !document.querySelector('[data-page="%s"] form');`, section, section)); err != nil {
				return fmt.Errorf("settings list-first %s: %w", section, err)
			}
		}
		if section == "About" {
			if err := p.probeAboutSettings(ctx, view, report, history); err != nil {
				return fmt.Errorf("about settings: %w", err)
			}
		}
	}
	if err := p.probeThemePicker(ctx, view, report); err != nil {
		return fmt.Errorf("appearance controls: %w", err)
	}
	if err := waitChatScript(ctx, page, `return !document.querySelector('[data-page="Service"]').hidden && !document.querySelector('.settings-content').disabled && (await mygo.call('DesktopSettings.Snapshot')).section === 'Service';`); err != nil {
		return err
	}
	// An inactive WebView can change activeElement without emitting focus/blur.
	// Give the real page the keyboard before exercising React's blur autosave.
	focused := make(chan struct{})
	p.window.Update(func() {
		p.showMainWindow()
		view.Focus()
		close(focused)
	})
	select {
	case <-focused:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := waitChatScript(ctx, page, `return document.hasFocus();`); err != nil {
		return fmt.Errorf("service input page focus: %w", err)
	}
	// Inputs autosave on blur; navigation must not drop the save or its command.
	if _, err := page.EvalContext(ctx, `
		window.smokeSetInput = (input, value) => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, value); input.dispatchEvent(new Event('input', {bubbles: true})); };
		window.smokeServiceBlurred = false;
		const input = document.querySelector('[data-page="Service"] input');
		input.addEventListener('blur', () => { window.smokeServiceBlurred = true; }, {once: true});
		input.focus();
		if (document.activeElement !== input) throw new Error('service input did not receive focus');
		smokeSetInput(input, 'localhost');
		await new Promise(resolve => setTimeout(resolve, 0));
		input.blur();
		document.querySelector('[data-section="People"]').click();
	`); err != nil {
		return err
	}
	if err := waitChatChecks(ctx, page, `
		const snapshot = await mygo.call('DesktopSettings.Snapshot');
		const checks = {
			blur: window.smokeServiceBlurred === true,
			saved: snapshot.settings.host === 'localhost',
			navigated: snapshot.section === 'People' && !document.querySelector('[data-page="People"]').hidden,
			idle: !document.querySelector('.settings-content').disabled
		};
		return {ready: Object.values(checks).every(Boolean), checks, host: snapshot.settings.host, focused: document.hasFocus()};
	`); err != nil {
		return fmt.Errorf("service input blur autosave/navigation: %w", err)
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-page="People"] [data-new-entity]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !!document.querySelector('[data-page="People"] form input');`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `
		const inputs = document.querySelectorAll('[data-page="People"] input');
		smokeSetInput(inputs[0], 'smoke-person'); smokeSetInput(inputs[1], 'Smoke Person'); smokeSetInput(inputs[2], 'smoke-password-123');
	`); err != nil {
		return err
	}
	// Submit in a later render, as an actual click would.
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-page="People"] form').requestSubmit();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return (await mygo.call('DesktopSettings.Snapshot')).users.some(u => u.username === 'smoke-person') && !document.querySelector('.settings-content').disabled && document.querySelector('[data-page="People"] input[type="password"]').value === ''`); err != nil {
		return fmt.Errorf("settings person save: %w", err)
	}
	users, err := p.service.Store.Users()
	if err != nil || len(users) != 1 {
		return fmt.Errorf("settings person not persisted: %v", err)
	}
	directory := filepath.Join(filepath.Dir(p.service.Store.DatabasePath()), "smoke-project")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-section="Projects"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !document.querySelector('[data-page="Projects"]').hidden && !document.querySelector('.settings-content').disabled`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-page="Projects"] [data-new-entity]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !!document.querySelector('[data-page="Projects"] form input');`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, fmt.Sprintf(`
		const inputs = document.querySelectorAll('[data-page="Projects"] input');
		smokeSetInput(inputs[0], 'Smoke Project'); smokeSetInput(inputs[1], %q);
		document.querySelectorAll('[data-page="Projects"] button[role="switch"]')[1].click();
	`, directory)); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-page="Projects"] form').requestSubmit();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return (await mygo.call('DesktopSettings.Snapshot')).projects.length === 1 && !document.querySelector('.settings-content').disabled`); err != nil {
		return fmt.Errorf("settings project form: %w", err)
	}
	if projects, err := p.service.Store.Projects(); err != nil || len(projects) != 1 || projects[0].Path != directory {
		return fmt.Errorf("settings project not persisted: %v", err)
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-updates", UpdatePreferences: workspace.UpdatePreferences{AutomaticChecks: false, AutomaticInstalls: false}}); err != nil {
		return err
	}
	if err := p.probeManagementLists(ctx, view, report); err != nil {
		return err
	}
	if err := p.probeOnlineAvatar(ctx, view, report); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-section="Service"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !document.querySelector('[data-page="Service"]').hidden && !document.querySelector('.settings-content').disabled && document.querySelector('[data-page="Service"] input').value === 'localhost'`); err != nil {
		return fmt.Errorf("settings autosave lost: %w", err)
	}
	if err := waitChatScript(ctx, page, `return (await mygo.call('DesktopSettings.Snapshot')).settings.host === 'localhost' && !document.querySelector('.settings-content').disabled`); err != nil {
		return fmt.Errorf("service field was not autosaved: %w", err)
	}
	if err := waitChatScript(ctx, page, `const section = document.querySelector('[data-page="Service"]'); return !section.querySelector('form, button[type="submit"]') && (await mygo.call('DesktopSettings.Snapshot')).status.RestartRequired && !!section.querySelector('[data-restart-service]');`); err != nil {
		return fmt.Errorf("service autosave/restart notice: %w", err)
	}
	for _, theme := range []string{"dark", "light"} {
		if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-appearance", Preferences: workspace.UIPreferences{Theme: theme, Language: "zh-CN"}}); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.documentElement.dataset.theme === %q && document.documentElement.lang === 'zh-CN' && document.querySelector('.settings-page-header h1').textContent === '服务' && document.documentElement.scrollWidth <= innerWidth`, theme)); err != nil {
			return err
		}
		if err := p.probeWindowChrome(ctx, view); err != nil {
			return err
		}
		if _, err := page.EvalContext(ctx, `
			await new Promise(resolve => setTimeout(resolve, 200));
			const primary = document.createElement('button'); primary.className = 'send'; document.body.appendChild(primary);
			const sendColor = getComputedStyle(primary).backgroundColor;
			const button = document.querySelector('.settings-primary');
			const toggle = document.querySelector('[data-page="Service"] [role="switch"][aria-checked="true"]');
			const same = getComputedStyle(button).backgroundColor === sendColor && getComputedStyle(toggle).backgroundColor === sendColor;
			primary.remove();
			if (!same) throw new Error('chat and settings primary colors differ');
			const ordinary = document.querySelector('[data-page="Service"] .settings-card-actions button:not(.settings-primary)');
			if (getComputedStyle(ordinary).color !== getComputedStyle(document.querySelector('.desktop-settings-shell')).color) throw new Error('ordinary button text must stay neutral');
			const scroll = getComputedStyle(document.querySelector('.settings-main'));
			if (CSS.supports('scrollbar-width', 'thin') && scroll.scrollbarWidth !== 'thin') throw new Error('settings scrollbar differs from chat');
			const footer = document.querySelector('.settings-sidebar-bottom');
			const reference = document.createElement('div'); reference.className = 'side-foot'; reference.style.cssText = 'position:fixed;left:-1000px;bottom:0;width:244px'; document.body.appendChild(reference);
			const bounds = footer.getBoundingClientRect();
			const sidebarBounds = document.querySelector('.settings-sidebar').getBoundingClientRect();
			const sameHeight = bounds.height === reference.getBoundingClientRect().height;
			reference.remove();
			const back = footer.querySelector('button'); const backBounds = back.getBoundingClientRect();
			const state = footer.querySelector('[role="status"]').getBoundingClientRect();
			if (!sameHeight || Math.abs(bounds.bottom - innerHeight) > 1 || Math.abs(bounds.left - sidebarBounds.left) > 1 || Math.abs(bounds.right - sidebarBounds.right) > 1 || backBounds.right >= state.left || state.right < bounds.right - 12) throw new Error('sidebar footer alignment differs from chat');
			if (back.textContent.trim() || footer.querySelectorAll('button').length !== 1 || document.querySelector('.settings-version')) throw new Error('footer must show only a back icon and service status');
			if (backBounds.width !== backBounds.height || getComputedStyle(back).borderRadius !== '50%') throw new Error('settings icon button must be circular');
		`); err != nil {
			return err
		}
		image, err := view.CapturePage()
		if err != nil {
			return err
		}
		if err := os.WriteFile(report+"-settings-"+theme+".png", image, 0o600); err != nil {
			return err
		}
	}
	resized := make(chan struct{})
	p.window.Update(func() { p.window.SetSize(840, 580); close(resized) })
	select {
	case <-resized:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := waitChatScript(ctx, page, `const main = document.querySelector('.settings-main'); const footer = document.querySelector('.settings-sidebar-bottom').getBoundingClientRect(); const sidebar = document.querySelector('.settings-sidebar').getBoundingClientRect(); return innerWidth <= 840 && main.scrollWidth <= main.clientWidth && document.documentElement.scrollWidth <= innerWidth && Math.abs(footer.bottom - innerHeight) <= 1 && Math.abs(footer.left - sidebar.left) <= 1 && Math.abs(footer.right - sidebar.right) <= 1;`); err != nil {
		return fmt.Errorf("compact settings layout: %w", err)
	}
	if image, err := view.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-settings-compact.png", image, 0o600); err != nil {
		return err
	}
	for _, section := range []string{"Projects", "Appearance", "About", "Service"} {
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return !document.querySelector('[data-section="%s"]').disabled;`, section)); err != nil {
			return fmt.Errorf("compact settings %s navigation available: %w", section, err)
		}
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-section="%s"]').click();`, section)); err != nil {
			return err
		}
		if err := waitChatChecks(ctx, page, fmt.Sprintf(`
			const snapshot = await mygo.call('DesktopSettings.Snapshot');
			const main = document.querySelector('.settings-main');
			const checks = {
				nativeSection: snapshot.section === %q,
				visible: !document.querySelector('[data-page="%s"]').hidden,
				idle: !document.querySelector('.settings-content').disabled && !document.querySelector('[data-section="%s"]').disabled,
				noOverflow: main.scrollWidth <= main.clientWidth
			};
			return {ready: Object.values(checks).every(Boolean), checks, section: snapshot.section,
				width: main.clientWidth, scrollWidth: main.scrollWidth};
		`, section, section, section)); err != nil {
			return fmt.Errorf("compact settings %s: %w", section, err)
		}
		if section == "About" {
			if err := waitChatScript(ctx, page, `
				const interval = document.querySelector('.settings-check-interval');
				const heading = interval.closest('.settings-row').querySelector('.settings-row-heading').getBoundingClientRect();
				const buttons = [...interval.querySelectorAll('button')].map(button => button.getBoundingClientRect());
				return buttons[0].left > heading.right && buttons.every(button => Math.abs(button.top - buttons[0].top) <= 1);
			`); err != nil {
				return fmt.Errorf("compact interval alignment: %w", err)
			}
			if image, err := view.CapturePage(); err != nil {
				return err
			} else if err := os.WriteFile(report+"-about-compact.png", image, 0o600); err != nil {
				return err
			}
		}
	}
	restored := make(chan struct{})
	p.window.Update(func() { p.window.SetSize(1280, 860); close(restored) })
	select {
	case <-restored:
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-restart-service]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `const snapshot = await mygo.call('DesktopSettings.Snapshot'); return snapshot.status.Running && !snapshot.status.RestartRequired && !document.querySelector('[data-restart-service]') && !document.querySelector('.settings-content').disabled;`); err != nil {
		return fmt.Errorf("explicit settings restart: %w", err)
	}
	// Restore the original listen address before chat's origin is established.
	settings, err := p.service.Store.Settings()
	if err != nil {
		return err
	}
	settings.Host = "127.0.0.1"
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-settings", Settings: settings}); err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "stop-service"}); err != nil {
		return err
	}
	return smokeSettingsCommand(ctx, page, settingsCommand{Kind: "start-service"})
}

func (p *panel) probeAboutSettings(ctx context.Context, view *mygo.WebView, report string, history *appupdate.ReleaseHistory) error {
	page := view.Page()
	if err := waitChatScript(ctx, page, `
		const section = document.querySelector('[data-page="About"]');
		const snapshot = await mygo.call('DesktopSettings.Snapshot');
		const link = section.querySelector('.settings-repository-link');
		const interval = section.querySelector('.settings-check-interval');
		const row = interval.closest('.settings-row');
		const heading = row.querySelector('.settings-row-heading').getBoundingClientRect();
		const choices = interval.getBoundingClientRect();
		return !section.hidden && !document.querySelector('[data-section="Updates"]') &&
			section.querySelectorAll('.settings-check-interval button').length === 5 &&
			!row.classList.contains('settings-row-wide') && choices.left > heading.right &&
			Math.abs((choices.top + choices.bottom) / 2 - (heading.top + heading.bottom) / 2) <= 1 &&
			section.textContent.includes(snapshot.version) &&
			link?.href === 'https://github.com/LarchLiu/spool' && !!link.querySelector('svg') &&
			section.lastElementChild.contains(link);
	`); err != nil {
		return err
	}
	for index, minutes := range []int{30, 60, 360, 720, 1440} {
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelectorAll('[data-page="About"] .settings-check-interval button')[%d].click();`, index)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`
			return (await mygo.call('DesktopSettings.Snapshot')).update_preferences.check_interval_minutes === %d &&
				document.querySelectorAll('[data-page="About"] .settings-check-interval button')[%d].getAttribute('aria-pressed') === 'true' &&
				!document.querySelector('.settings-content').disabled;
		`, minutes, index)); err != nil {
			return fmt.Errorf("interval %d: %w", minutes, err)
		}
	}
	if image, err := view.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-about.png", image, 0o600); err != nil {
		return err
	}
	if err := p.probeUpdateIndicators(ctx, view, report, history); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-page="About"] [role="switch"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `
		const preferences = (await mygo.call('DesktopSettings.Snapshot')).update_preferences;
		return !preferences.automatic_checks && preferences.check_interval_minutes === 1440 &&
			[...document.querySelectorAll('[data-page="About"] .settings-check-interval button')].every(button => button.disabled) &&
			!document.querySelector('.settings-content').disabled;
	`); err != nil {
		return err
	}
	return smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-updates", UpdatePreferences: workspace.UpdatePreferences{
		AutomaticChecks: true, CheckIntervalMinutes: workspace.DefaultUpdateCheckIntervalMinutes,
	}})
}

func (p *panel) probeUpdateIndicators(ctx context.Context, view *mygo.WebView, report string, history *appupdate.ReleaseHistory) error {
	// Exercise the real bridge and preload without contacting an update server
	// or downloading/installing a release in the smoke process.
	checks := 0
	if history == nil {
		history = &appupdate.ReleaseHistory{CurrentVersion: desktopVersion(), TargetVersion: "99.0.0", Releases: []appupdate.ReleaseNotes{
			{Version: "99.0.0", Body: "## Features\n- **Markdown** release notes\n\n| Feature | Status |\n| --- | --- |\n| Release history | Ready |", URL: "https://github.com/LarchLiu/spool/releases/tag/v99.0.0"},
			{Version: "98.0.0", Body: "## Fixes\n- Preserve `settings` preferences", URL: "https://github.com/LarchLiu/spool/releases/tag/v98.0.0"},
		}}
	}
	var notesRequests atomic.Int32
	fixture := appupdate.New(appupdate.Options{
		Enabled: true,
		Check: func(context.Context) (*appupdate.Release, error) {
			checks++
			if checks == 1 {
				return &appupdate.Release{Version: history.TargetVersion}, nil
			}
			return nil, nil
		},
		ReleaseNotes: func(context.Context, string, string) ([]appupdate.ReleaseNotes, error) {
			notesRequests.Add(1)
			return history.Releases, nil
		},
	})
	defer fixture.Close()
	fixture.Check()
	original := make(chan *appupdate.Controller, 1)
	var previousVersion string
	p.window.Update(func() {
		previousVersion = mygo.App.Version()
		mygo.App.SetVersion(history.CurrentVersion)
		original <- p.updates
		p.updates = fixture
		p.sendUpdateAvailability(p.chatView)
	})
	var previous *appupdate.Controller
	select {
	case previous = <-original:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer p.window.Update(func() {
		mygo.App.SetVersion(previousVersion)
		p.updates = previous
		p.sendUpdateAvailability(p.chatView)
	})
	for _, available := range []bool{true, false} {
		if !available {
			fixture.Check()
			p.window.Update(func() { p.sendUpdateAvailability(p.chatView) })
		}
		if err := waitChatScript(ctx, view.Page(), fmt.Sprintf(`
			return !!document.querySelector('[data-section="About"] .update-indicator') === %t;
		`, available)); err != nil {
			return fmt.Errorf("about update dot %t: %w", available, err)
		}
		if err := waitChatScript(ctx, p.chatView.Page(), fmt.Sprintf(`
			return window.spoolUpdateAvailable === %t && !!document.querySelector('a.desktop-settings .update-indicator') === %t;
		`, available, available)); err != nil {
			return fmt.Errorf("chat update dot %t: %w", available, err)
		}
		if available {
			if notesRequests.Load() != 0 {
				return fmt.Errorf("release notes fetched before expansion")
			}
			if err := p.probeReleaseHistory(ctx, view, report, len(history.Releases)); err != nil {
				return err
			}
			if notesRequests.Load() != 1 {
				return fmt.Errorf("release notes collapse/reopen lost its cache: %d requests", notesRequests.Load())
			}
			if image, err := view.CapturePage(); err != nil {
				return err
			} else if err := os.WriteFile(report+"-about-update.png", image, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *panel) probeReleaseHistory(ctx context.Context, view *mygo.WebView, report string, count int) error {
	page := view.Page()
	if _, err := page.EvalContext(ctx, `document.querySelector('.settings-release-toggle').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, fmt.Sprintf(`
		return document.querySelector('.settings-release-toggle')?.getAttribute('aria-expanded') === 'true' &&
			document.querySelectorAll('.settings-release-version').length === %d &&
			!!document.querySelector('.settings-release-markdown h2, .settings-release-markdown h3') &&
			!!document.querySelector('.settings-release-markdown li') &&
			!document.querySelector('.settings-release-body [role="alert"]');
	`, count)); err != nil {
		return fmt.Errorf("release history rendering: %w", err)
	}
	// Use the real settings preferences and renderer, with an isolated fixture
	// behind the native bridge. Never fetch or install an update in this probe.
	preferences, err := p.service.Store.UIPreferences("")
	if err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-appearance", Preferences: workspace.UIPreferences{Theme: "light", Language: "zh-CN"}}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.documentElement.dataset.theme === 'light' && document.querySelector('.settings-release-toggle').textContent.includes('查看版本更新信息');`); err != nil {
		return err
	}
	// Verify collapse/reopen retains already loaded content.
	for _, expanded := range []bool{false, true} {
		if _, err := page.EvalContext(ctx, `document.querySelector('.settings-release-toggle').click();`); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.querySelector('.settings-release-toggle').getAttribute('aria-expanded') === '%t';`, expanded)); err != nil {
			return err
		}
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('.settings-release-history').scrollIntoView({block: 'start'});`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `const bounds = document.querySelector('.settings-release-history').getBoundingClientRect(); return bounds.top >= -1 && bounds.top < innerHeight / 2;`); err != nil {
		return err
	}
	for i, suffix := range []string{"-release-history.png", "-release-history-older.png"} {
		if i != 0 {
			if _, err := page.EvalContext(ctx, `document.querySelectorAll('.settings-release-version')[1].scrollIntoView({block: 'start'});`); err != nil {
				return err
			}
		}
		time.Sleep(200 * time.Millisecond)
		image, err := view.CapturePage()
		if err != nil {
			return err
		}
		if err := os.WriteFile(report+suffix, image, 0o600); err != nil {
			return err
		}
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('.settings-main').scrollTop = 0;`); err != nil {
		return err
	}
	return smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-appearance", Preferences: preferences})
}

func (p *panel) probeThemePicker(ctx context.Context, view *mygo.WebView, report string) error {
	page := view.Page()
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-appearance", Preferences: workspace.UIPreferences{Theme: "system", Language: "zh-CN"}}); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-section="Appearance"]').click();`); err != nil {
		return err
	}
	if err := waitChatChecks(ctx, page, `
		const section = document.querySelector('[data-page="Appearance"]');
		const language = section?.querySelector('.custom-select-trigger[aria-haspopup="listbox"]');
		const checks = {
			visible: !!section && !section.hidden,
			idle: !!document.querySelector('.settings-content') && !document.querySelector('.settings-content').disabled,
			themes: section?.querySelectorAll('.settings-theme-picker button').length === 3,
			language: !!language && !language.matches(':disabled') && language.getAttribute('aria-expanded') === 'false'
		};
		return {ready: Object.values(checks).every(Boolean), checks};
	`); err != nil {
		return fmt.Errorf("appearance render: %w", err)
	}
	if err := p.probeSettingsLanguage(ctx, page); err != nil {
		return err
	}
	for _, theme := range []string{"light", "dark", "system"} {
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-theme-choice="%s"]').click();`, theme)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`
			const snapshot = await mygo.call('DesktopSettings.Snapshot');
			const selected = document.querySelector('[data-theme-choice="%s"]');
			const bounds = selected.getBoundingClientRect();
			return !document.querySelector('.settings-content').disabled && snapshot.preferences.theme === %q &&
				snapshot.preferences.language === 'zh-CN' && selected.getAttribute('aria-pressed') === 'true' &&
				document.querySelectorAll('.settings-theme-picker [aria-pressed="true"]').length === 1 &&
				bounds.width === bounds.height && getComputedStyle(selected).borderRadius === '50%%';
		`, theme, theme)); err != nil {
			return fmt.Errorf("settings theme icon %s: %w", theme, err)
		}
		if theme != "system" {
			if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.documentElement.dataset.theme === %q;`, theme)); err != nil {
				return err
			}
			if image, err := view.CapturePage(); err != nil {
				return err
			} else if err := os.WriteFile(report+"-appearance-"+theme+".png", image, 0o600); err != nil {
				return err
			}
		}
	}
	_, err := page.EvalContext(ctx, `document.querySelector('[data-section="Service"]').click();`)
	return err
}

func (p *panel) probeSettingsLanguage(ctx context.Context, page *mygo.Page) error {
	for _, language := range []string{"ja", "en", "zh-CN"} {
		if _, err := page.EvalContext(ctx, `
			const trigger = document.querySelector('[data-page="Appearance"] .custom-select-trigger[aria-haspopup="listbox"]');
			trigger.scrollIntoView({block: 'center'});
			trigger.click();
		`); err != nil {
			return fmt.Errorf("language %s open: %w", language, err)
		}
		if err := waitChatChecks(ctx, page, fmt.Sprintf(`
			const trigger = document.querySelector('[data-page="Appearance"] .custom-select-trigger');
			const menu = document.getElementById(trigger.getAttribute('aria-controls'));
			const option = menu?.querySelector('[role="option"][data-value="%s"]');
			const bounds = menu?.getBoundingClientRect();
			const checks = {
				open: trigger.getAttribute('aria-expanded') === 'true' && menu?.getAttribute('role') === 'listbox',
				option: !!option && !option.matches(':disabled'),
				viewport: !!bounds && bounds.width > 0 && bounds.height > 0 && bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight,
				unclipped: !!bounds && !!menu?.contains(document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2))
			};
			return {ready: Object.values(checks).every(Boolean), checks, bounds: bounds?.toJSON()};
		`, language)); err != nil {
			return fmt.Errorf("language %s popup: %w", language, err)
		}
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('.custom-select-menu [role="option"][data-value="%s"]').click();`, language)); err != nil {
			return fmt.Errorf("language %s select: %w", language, err)
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`
			const snapshot = await mygo.call('DesktopSettings.Snapshot');
			const trigger = document.querySelector('[data-page="Appearance"] .custom-select-trigger');
			return snapshot.preferences.language === %q && snapshot.preferences.theme === 'system' &&
				document.documentElement.lang === %q && !document.querySelector('.settings-content').disabled &&
				!trigger.matches(':disabled') && trigger.getAttribute('aria-expanded') === 'false' &&
				!document.querySelector('.custom-select-menu');
		`, language, language)); err != nil {
			return fmt.Errorf("language %s save: %w", language, err)
		}
	}
	return nil
}

func (p *panel) probeManagementLists(ctx context.Context, view *mygo.WebView, report string) error {
	page := view.Page()
	for _, section := range []string{"People", "Projects", "Agents"} {
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-section="%s"]').click();`, section)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return !document.querySelector('[data-page="%s"]').hidden && !document.querySelector('.settings-content').disabled;`, section)); err != nil {
			return err
		}
		// Created entries remain in their editor until Back; no tabs or editors
		// are mixed into the overview list.
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-page="%s"] .settings-back')?.click();`, section)); err != nil {
			return err
		}
		count := 1
		if section == "Agents" {
			count = 2
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.querySelectorAll('[data-page="%s"] .settings-list-item').length === %d && !document.querySelector('[data-page="%s"] form');`, section, count, section)); err != nil {
			return err
		}
		if section == "People" {
			if err := waitChatScript(ctx, page, `const avatar = document.querySelector('[data-page="People"] .settings-list-icon .user-avatar'); return avatar?.complete && avatar.naturalWidth > 0 && avatar.width === 34;`); err != nil {
				return fmt.Errorf("people list avatar: %w", err)
			}
		}
		if section == "Projects" {
			if _, err := page.EvalContext(ctx, `const panel = document.getElementById('github-cli-path-settings'); const toggle = document.querySelector('[aria-controls="github-cli-path-settings"]'); if (!panel?.hidden || toggle?.getAttribute('aria-expanded') !== 'false' || panel.getClientRects().length) throw new Error('global GitHub CLI path must start collapsed'); toggle.click();`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `const panel = document.getElementById('github-cli-path-settings'); return !panel.hidden && !!panel.querySelector('input')?.getClientRects().length && document.querySelector('[aria-controls="github-cli-path-settings"]').getAttribute('aria-expanded') === 'true';`); err != nil {
				return fmt.Errorf("GitHub CLI path did not expand: %w", err)
			}
			if _, err := page.EvalContext(ctx, `smokeSetInput(document.querySelector('#github-cli-path-settings input'), '/smoke/global-gh-draft');`); err != nil {
				return err
			}
			if _, err := page.EvalContext(ctx, `document.querySelector('[aria-controls="github-cli-path-settings"]').click();`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return document.getElementById('github-cli-path-settings').hidden && document.querySelector('[aria-controls="github-cli-path-settings"]').getAttribute('aria-expanded') === 'false';`); err != nil {
				return fmt.Errorf("GitHub CLI path did not collapse: %w", err)
			}
			if _, err := page.EvalContext(ctx, `const status = document.querySelector('[data-page="Projects"] .settings-github-cli .settings-row-control'); if (status.lastElementChild?.getAttribute('aria-controls') !== 'github-cli-path-settings') throw new Error('path settings must be the rightmost status action');`); err != nil {
				return err
			}
		}
		if section == "Agents" {
			if _, err := page.EvalContext(ctx, `const icons = [...document.querySelectorAll('[data-page="Agents"] .settings-list-icon .agent-icon')]; if (icons.length !== 2 || icons[0].innerHTML === icons[1].innerHTML) throw new Error('agent rows must show distinct CLI icons');`); err != nil {
				return err
			}
		}
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-page="%s"] .settings-list-item').click();`, section)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return !!document.querySelector('[data-page="%s"] .settings-back') && !!document.querySelector('[data-page="%s"] form input') && !document.querySelector('[data-page="%s"] .settings-list');`, section, section, section)); err != nil {
			return err
		}
		if section == "Projects" {
			if _, err := page.EvalContext(ctx, `if (document.querySelector('[data-page="Projects"] .settings-github-cli')) throw new Error('global GitHub CLI leaked into a project editor');`); err != nil {
				return err
			}
		}
		// Edit an existing item through the actual UI, and prove Save updates
		// it rather than creating another person/project or losing its ID.
		script := ""
		switch section {
		case "People":
			script = `const inputs = document.querySelectorAll('[data-page="People"] input'); if (inputs[0].value !== 'smoke-person' || inputs[2].value !== '') throw new Error('person editor did not load safely'); smokeSetInput(inputs[1], 'Smoke Person Updated');`
		case "Projects":
			script = `const input = document.querySelector('[data-page="Projects"] input'); if (input.value !== 'Smoke Project') throw new Error('project editor did not load'); smokeSetInput(input, 'Smoke Project Updated');`
		case "Agents":
			script = `const toggle = document.querySelector('[data-page="Agents"] [role="switch"]'); if (toggle.getAttribute('aria-checked') === 'true') toggle.click();`
		}
		if _, err := page.EvalContext(ctx, script); err != nil {
			return err
		}
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-page="%s"] form').requestSubmit();`, section)); err != nil {
			return err
		}
		condition := map[string]string{
			"People":   `snapshot.users.length === 1 && snapshot.users[0].nickname === 'Smoke Person Updated'`,
			"Projects": `snapshot.projects.length === 1 && snapshot.projects[0].name === 'Smoke Project Updated'`,
			"Agents":   `snapshot.agents.find(agent => agent.backend === 'codex').enabled === false`,
		}[section]
		if err := waitChatScript(ctx, page, `const snapshot = await mygo.call('DesktopSettings.Snapshot'); return !document.querySelector('.settings-content').disabled && `+condition+`;`); err != nil {
			return fmt.Errorf("settings edit %s: %w", section, err)
		}
		if _, err := page.EvalContext(ctx, fmt.Sprintf(`document.querySelector('[data-page="%s"] .settings-back').click();`, section)); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.querySelectorAll('[data-page="%s"] .settings-list-item').length === %d && !document.querySelector('[data-page="%s"] form');`, section, count, section)); err != nil {
			return err
		}
		if section == "Projects" {
			if _, err := page.EvalContext(ctx, `document.querySelector('[aria-controls="github-cli-path-settings"]').click();`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return !document.getElementById('github-cli-path-settings').hidden && document.querySelector('[data-page="Projects"] .settings-github-cli input')?.value === '/smoke/global-gh-draft';`); err != nil {
				return fmt.Errorf("global GitHub CLI draft lost across project editing: %w", err)
			}
			if _, err := page.EvalContext(ctx, `smokeSetInput(document.querySelector('[data-page="Projects"] .settings-github-cli input'), ''); await new Promise(resolve => setTimeout(resolve, 200));`); err != nil {
				return err
			}
			if image, err := view.CapturePage(); err != nil {
				return err
			} else if err := os.WriteFile(report+"-github-path-expanded.png", image, 0o600); err != nil {
				return err
			}
			if _, err := page.EvalContext(ctx, `document.querySelector('[aria-controls="github-cli-path-settings"]').click();`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return document.getElementById('github-cli-path-settings').hidden;`); err != nil {
				return err
			}
		}
		for _, theme := range []string{"dark", "light"} {
			if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-appearance", Preferences: workspace.UIPreferences{Theme: theme, Language: "zh-CN"}}); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.documentElement.dataset.theme === %q && document.documentElement.lang === 'zh-CN' && !document.querySelector('.settings-content').disabled;`, theme)); err != nil {
				return err
			}
			if _, err := page.EvalContext(ctx, `await new Promise(resolve => setTimeout(resolve, 200)); const main = document.querySelector('.settings-main'); if (main.scrollWidth > main.clientWidth) throw new Error('management list overflows');`); err != nil {
				return err
			}
			image, err := view.CapturePage()
			if err != nil {
				return err
			}
			if err := os.WriteFile(report+"-list-"+section+"-"+theme+".png", image, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *panel) probeOnlineAvatar(ctx context.Context, view *mygo.WebView, report string) error {
	address, err := p.service.LocalWebURL()
	if err != nil {
		return err
	}
	// Use an isolated HTTP cookie jar, never the system WebView's shared cookies.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	post := func(path, body, csrf string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", address+path, bytes.NewBufferString(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Origin", address)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("online avatar smoke %s: HTTP %d: %s", path, res.StatusCode, data)
		}
		return data, err
	}
	data, err := post("/api/login", `{"username":"smoke-person","password":"smoke-password-123"}`, "")
	if err != nil {
		return err
	}
	var identity struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return err
	}
	defer func() { _, _ = post("/api/logout", `{}`, identity.CSRF) }()
	if _, err := post("/api/presence", `{"client_id":"desktop-smoke","visible":true}`, identity.CSRF); err != nil {
		return err
	}
	page := view.Page()
	if _, err := page.EvalContext(ctx, `document.querySelector('[data-section="Online"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `const section = document.querySelector('[data-page="Online"]'); const avatar = section.querySelector('.settings-row-icon .user-avatar'); const person = document.querySelector('[data-page="People"] .user-avatar'); return !section.hidden && avatar?.complete && avatar.naturalWidth > 0 && avatar.src === person?.src && section.textContent.includes('Smoke Person Updated');`); err != nil {
		return fmt.Errorf("online avatar does not match people: %w", err)
	}
	if _, err := page.EvalContext(ctx, `await new Promise(resolve => setTimeout(resolve, 200));`); err != nil {
		return err
	}
	image, err := view.CapturePage()
	if err != nil {
		return err
	}
	return os.WriteFile(report+"-online-avatar.png", image, 0o600)
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
		const login = !!document.querySelector('.desktop-login');
		const position = login
			? bounds?.top <= 22 && bounds?.right >= innerWidth - 22 && bounds?.left > innerWidth / 2
			: bounds?.left <= 22 && bounds?.bottom >= innerHeight - 22 && bounds?.right < innerWidth / 2;
		return Math.abs(innerWidth - %d) <= 2 && Math.abs(innerHeight - %d) <= 2 &&
			position && getComputedStyle(link).position === 'fixed' &&
			document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)?.closest('a') === link;
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
			return fmt.Errorf("desktop chat did not become ready: %w (last result %v, %v; script: %s)", ctx.Err(), value, err, script)
		case <-ticker.C:
		}
	}
}

// Preserve layout measurements when a platform-specific assertion times out.
func waitChatChecks(ctx context.Context, page *mygo.Page, script string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last any
	var lastErr error
	for {
		value, err := page.EvalContext(ctx, script)
		if err == nil {
			last = value
		}
		lastErr = err
		if result, ok := value.(map[string]any); err == nil && ok && result["ready"] == true {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("desktop chat layout did not become ready: %w (last result %v, %v)", ctx.Err(), last, lastErr)
		case <-ticker.C:
		}
	}
}
