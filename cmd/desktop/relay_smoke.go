package main

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"spool/internal/workspace"
	"spool/relay"
)

// Exercise the actual Desktop settings page, its native bridge and persisted
// settings. The release smoke owns an isolated data directory; no public relay
// is contacted and the screenshot contains neither credentials nor a QR code.
func (p *panel) probeRelaySettings(ctx context.Context, view *mygo.WebView, report string) error {
	settings, err := p.service.Store.Settings()
	if err != nil {
		return err
	}
	preferences, err := p.service.Store.UIPreferences("")
	if err != nil {
		return err
	}
	if err := p.smokePresentation(ctx, workspace.UIPreferences{Theme: "light", Language: "zh-CN"}); err != nil {
		return err
	}
	focused := make(chan struct{})
	p.window.Update(func() { p.showMainWindow(); view.Focus(); close(focused) })
	select {
	case <-focused:
	case <-ctx.Done():
		return ctx.Err()
	}
	page := view.Page()
	if err := waitChatScript(ctx, page, `
		return document.hasFocus() && document.documentElement.lang === 'zh-CN' &&
			smokeSettingsReady('Service') && !!document.querySelector('input[aria-label="中继 HTTPS 地址"]');
	`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `
		document.querySelector('[data-service-tab="relay"]').click();
		await new Promise(resolve => setTimeout(resolve, 0));
		const input = document.querySelector('input[aria-label="中继 HTTPS 地址"]');
		input.focus();
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, 'https://relay.example.com');
		input.dispatchEvent(new Event('input', {bubbles: true}));
		await new Promise(resolve => setTimeout(resolve, 0));
		input.blur();
	`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `
		const snapshot = await mygo.call('DesktopSettings.Snapshot');
		return snapshot.settings.relay_url === 'https://relay.example.com' && !snapshot.settings.relay_enabled && smokeSettingsReady('Service');
	`); err != nil {
		return fmt.Errorf("address blur autosave: %w", err)
	}
	if stored, err := p.service.Store.Settings(); err != nil || stored.RelayURL != "https://relay.example.com" {
		return fmt.Errorf("relay URL was not persisted: %v", err)
	}
	if _, err := page.EvalContext(ctx, `
		const input = document.querySelector('input[aria-label="中继 HTTPS 地址"]');
		const group = input.closest('.settings-group');
		group.scrollIntoView({block: 'start', behavior: 'instant'});
		document.querySelector('.settings-main').scrollBy(0, -72);
		await new Promise(resolve => setTimeout(resolve, 250));
		const bounds = input.getBoundingClientRect();
		if (bounds.top < 0 || bounds.bottom > innerHeight || bounds.right > innerWidth) throw new Error('relay address is outside the viewport');
		const registration = document.querySelector('input[aria-label="中继注册令牌"]');
		const toggle = document.querySelector('[role="switch"][aria-label="启用 Spool 中继"]');
		if (!registration || !toggle?.disabled || toggle.getAttribute('aria-checked') !== 'false') throw new Error('unregistered relay switch is not locked');
		if (!group.querySelector('.settings-card').firstElementChild.contains(toggle)) throw new Error('relay switch must stay first');
		if (!input.closest('[role="group"]').contains(registration)) throw new Error('registration must be inside relay address group');
		const buttons = registration.closest('[role="group"]').querySelectorAll('button');
		if (buttons.length !== 1 || buttons[0].textContent !== '注册') throw new Error('registration must have one action');
		const pairingButton = document.querySelector('button.settings-relay-disclosure[aria-label="配对 Android 手机"]');
		if (pairingButton?.closest('.settings-group') !== group) throw new Error('phone pairing must belong to relay settings');
	`); err != nil {
		return err
	}
	image, err := view.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(report+"-relay-settings.png", image, 0600); err != nil {
		return err
	}
	// Exercise registration against a disposable loopback relay through the real
	// private bridge. Enrollment must leave ingress off until explicitly enabled.
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("s", 32), DataDir: filepath.Join(p.service.Store.Root(), "smoke-relay"), AllowUnregisteredHosts: true})
	if err != nil {
		return err
	}
	public := httptest.NewServer(broker)
	defer func() { broker.Close(); public.Close() }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	local := settings
	local.RelayURL = public.URL
	local.RelayLANPort = listener.Addr().(*net.TCPAddr).Port
	local.RelayEnabled = false
	listener.Close()
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-settings", Settings: local}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, fmt.Sprintf(`return document.querySelector('input[aria-label="中继 HTTPS 地址"]').value === %q && smokeSettingsReady('Service');`, public.URL)); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('input[aria-label="中继注册令牌"]').closest('[role="group"]').querySelector('button').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `
		const snapshot = await mygo.call('DesktopSettings.Snapshot');
		const toggle = document.querySelector('[role="switch"][aria-label="启用 Spool 中继"]');
		return snapshot.status.Relay.Registered && snapshot.status.Relay.RegistrationTokenSet && !snapshot.settings.relay_enabled &&
			!document.querySelector('input[aria-label="中继注册令牌"]') && !toggle.disabled && toggle.getAttribute('aria-checked') === 'false';
	`); err != nil {
		return fmt.Errorf("relay registration: %w", err)
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('[role="switch"][aria-label="启用 Spool 中继"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return (await mygo.call('DesktopSettings.Snapshot')).settings.relay_enabled && smokeSettingsReady('Service');`); err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "apply-service-changes", Target: "relay"}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `
		const snapshot = await mygo.call('DesktopSettings.Snapshot');
		const toggle = document.querySelector('[role="switch"][aria-label="启用 Spool 中继"]');
		const status = document.querySelector('[aria-label="中继状态"]');
		return snapshot.status.Relay.State === 'connected' && status?.textContent.includes('已连接') &&
			status.closest('.settings-row') === toggle.closest('.settings-row') &&
			status.getBoundingClientRect().right <= toggle.getBoundingClientRect().left && !document.querySelector('input[aria-label="中继注册令牌"]');
	`); err != nil {
		return fmt.Errorf("relay inline status: %w", err)
	}
	image, err = view.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(report+"-relay-connected.png", image, 0600); err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "save-settings", Settings: settings}); err != nil {
		return err
	}
	if err := smokeSettingsCommand(ctx, page, settingsCommand{Kind: "apply-service-changes", Target: "relay"}); err != nil {
		return err
	}
	if err := p.smokePresentation(ctx, preferences); err != nil {
		return err
	}
	_, err = page.EvalContext(ctx, `document.querySelector('[data-service-tab="web"]').click(); document.querySelector('.settings-main').scrollTo(0, 0);`)
	return err
}
