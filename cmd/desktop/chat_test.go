package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"testing"

	"github.com/egoist/mygo/ui"

	"spool/internal/workspace"
)

func TestLaunchShowsChatOnlyAfterBackgroundServiceStarts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		autoStart bool
		blocked   bool
	}{
		{"normal launch", true, false},
		{"auto-start disabled", false, false},
		{"service port occupied", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, err := workspace.OpenService(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			settings := workspace.DefaultSettings()
			settings.AutoStart = tc.autoStart
			settings.Port = listener.Addr().(*net.TCPAddr).Port
			if !tc.blocked {
				_ = listener.Close()
			}
			if err := service.Store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			p := &panel{service: service, settings: settings, tab: "Service"}
			p.startOnLaunch()
			wantRunning := tc.autoStart && !tc.blocked
			if service.Status().Running != wantRunning {
				t.Fatal("opening chat changed background service startup", service.Status())
			}
			wantTab := "Service"
			if wantRunning {
				wantTab = "Chat"
			}
			if p.tab != wantTab {
				t.Fatalf("initial tab = %q, want %q", p.tab, wantTab)
			}
			if tc.blocked && p.message == "" {
				t.Fatal("startup failure was not exposed in settings")
			}
		})
	}
}

func TestChatNavigationStaysOnExactServiceOrigin(t *testing.T) {
	for _, tc := range []struct {
		target string
		allow  bool
	}{
		{"http://127.0.0.1:8787/", true},
		{"http://127.0.0.1:8787/#turn", true},
		{"http://127.0.0.1:8787/api/chats/one/attachments/two", true},
		{"http://127.0.0.1:8788/", false},
		{"http://localhost:8787/", false},
		{"https://127.0.0.1:8787/", false},
		{"http://127.0.0.1:8787.evil.example/", false},
		{"http://127.0.0.1:8787@evil.example/", false},
		{"http://user@127.0.0.1:8787/", false},
		{"https://github.com/owner/repo/pull/1", false},
		{"file:///etc/passwd", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hello", false},
		{"about:blank", false},
		{"/", false},
		{"%", false},
	} {
		if got := chatNavigationAllowed("http://127.0.0.1:8787", tc.target); got != tc.allow {
			t.Errorf("navigation %q = %v, want %v", tc.target, got, tc.allow)
		}
	}
	if !chatNavigationAllowed("http://[::1]:8787", "http://[::1]:8787/#turn") {
		t.Fatal("IPv6 loopback navigation rejected")
	}
	if chatNavigationAllowed("", "http://127.0.0.1:8787") {
		t.Fatal("navigation allowed before the service origin was set")
	}
}

func TestChatExternalLinksOnlyOpenWebURLs(t *testing.T) {
	for _, address := range []string{"http://example.com/", "https://github.com/owner/repo/pull/1", "http://[::1]:8787/"} {
		if !chatExternalLinkAllowed(address) {
			t.Errorf("Web link %q rejected", address)
		}
	}
	for _, address := range []string{"", "/api/chats/one", "//evil.example", "file:///tmp/a", "javascript:alert(1)", "data:text/html,hello", "mailto:a@example.com", "mygo://localhost/", "https://user:secret@example.com/", "https:", "%"} {
		if chatExternalLinkAllowed(address) {
			t.Errorf("unsafe external link %q allowed", address)
		}
	}
}

func TestChatSettingsLinkOnlyInterceptsExactLocalDestination(t *testing.T) {
	for _, tc := range []struct {
		target string
		allow  bool
	}{
		{"http://127.0.0.1:8787/__desktop/settings", true},
		{"http://127.0.0.1:8788/__desktop/settings", false},
		{"https://127.0.0.1:8787/__desktop/settings", false},
		{"http://localhost:8787/__desktop/settings", false},
		{"http://user@127.0.0.1:8787/__desktop/settings", false},
		{"https://example.com/__desktop/settings", false},
		{"http://127.0.0.1:8787/__desktop/settings/", false},
		{"http://127.0.0.1:8787/__desktop/settings?", false},
		{"http://127.0.0.1:8787/__desktop/settings?tab=People", false},
		{"http://127.0.0.1:8787/__desktop/settings#People", false},
		{"http://127.0.0.1:8787/__desktop/%73ettings", false},
		{"http://127.0.0.1:8787/api/settings", false},
		{"/__desktop/settings", false},
		{"javascript:alert(1)", false},
		{"%", false},
	} {
		if got := chatSettingsNavigation("http://127.0.0.1:8787", tc.target); got != tc.allow {
			t.Errorf("settings navigation %q = %v, want %v", tc.target, got, tc.allow)
		}
	}
	if !chatSettingsNavigation("http://[::1]:8787", "http://[::1]:8787/__desktop/settings") {
		t.Fatal("IPv6 settings navigation rejected")
	}
	if chatSettingsNavigation("", "http://127.0.0.1:8787/__desktop/settings") {
		t.Fatal("settings navigation allowed before service origin was set")
	}
}

func TestChatWebViewUsesPreloadWithoutNativePrivileges(t *testing.T) {
	opts := chatWebViewOptions()
	if opts.URL != "" {
		t.Fatal("navigation must wait until the embedded page guards are installed")
	}
	if len(opts.Page.TrustedOrigins) != 0 || opts.Page.PreloadScript == "" {
		t.Fatal("desktop hint must not grant access to bound native methods")
	}
}

func TestChatSessionChangeHintOnlyInterceptsExactLocalDestination(t *testing.T) {
	const origin = "http://127.0.0.1:8787"
	for _, tc := range []struct {
		target string
		allow  bool
	}{
		{origin + desktopSessionChangedPath, true},
		{"http://127.0.0.1:8788" + desktopSessionChangedPath, false},
		{"http://localhost:8787" + desktopSessionChangedPath, false},
		{"https://evil.test" + desktopSessionChangedPath, false},
		{origin + desktopSessionChangedPath + "?user=user-12345678", false},
		{origin + desktopSessionChangedPath + "#fragment", false},
		{origin + desktopSessionChangedPath + "/", false},
		{origin + "/__desktop/%73ession-changed", false},
		{origin + desktopSettingsPath, false},
	} {
		if got := chatSessionChangedNavigation(origin, tc.target); got != tc.allow {
			t.Errorf("session hint %q = %v, want %v", tc.target, got, tc.allow)
		}
	}
}

func TestDesktopViewedSessionHintValidatesOriginAndPresentationFields(t *testing.T) {
	const origin = "http://127.0.0.1:8787"
	for _, test := range []struct {
		target, chat string
		changed, ok  bool
	}{
		{target: origin + desktopViewPath + "?chat=", ok: true},
		{target: origin + desktopViewPath + "?chat=chat-12345678", chat: "chat-12345678", ok: true},
		{target: origin + desktopViewPath + "?chat=chat-12345678&session=changed", chat: "chat-12345678", changed: true, ok: true},
		{target: origin + desktopViewPath + "?chat=&session=changed", changed: true, ok: true},
		{target: "https://evil.test" + desktopViewPath + "?chat=chat-12345678"},
		{target: "http://127.0.0.1:8788" + desktopViewPath + "?chat=chat-12345678"},
		{target: origin + desktopViewPath + "?chat=short"},
		{target: origin + desktopViewPath + "?chat=chat-12345678&chat=chat-87654321"},
		{target: origin + desktopViewPath + "?chat=chat-12345678&user=user-12345678"},
		{target: origin + desktopViewPath + "?chat=chat-12345678&session=changed&user=user-12345678"},
		{target: origin + desktopViewPath + "?chat=chat-12345678&session=changed&session=changed"},
		{target: origin + desktopViewPath + "?chat=chat-12345678&session=unknown"},
		{target: origin + desktopViewPath + "?chat=chat-12345678#fragment"},
		{target: origin + "/__desktop/%76iew?chat=chat-12345678"},
		{target: origin + desktopViewPath + "?session=changed"},
	} {
		view, ok := desktopViewNavigation(origin, test.target)
		if ok != test.ok || view.chat != test.chat || view.sessionChanged != test.changed {
			t.Errorf("view hint %q = %+v, %v", test.target, view, ok)
		}
	}
}

func TestChatEmbedsInMainWindowInsteadOfOpeningAnotherWindow(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "chat.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	embedded := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if method.Sel.Name == "NewWindow" {
			t.Error("chat must not create a top-level window")
		}
		if method.Sel.Name == "NewWebView" {
			embedded = true
		}
		return true
	})
	if !embedded {
		t.Fatal("chat does not create an embedded WebView")
	}
}

func TestSelectingNativeTabDoesNotResetChatState(t *testing.T) {
	p := &panel{tab: "Chat", chatURL: "http://127.0.0.1:8787", chatStarting: true, message: "previous message"}
	p.selectTab("Service")
	if p.tab != "Service" || p.message != "" || p.chatURL != "http://127.0.0.1:8787" || !p.chatStarting {
		t.Fatalf("tab switch reset chat state: %+v", p)
	}
}

func TestChatLayoutReplacesNativeSidebar(t *testing.T) {
	p := &panel{tab: "Chat", preferences: workspace.UIPreferences{Language: "en"}}
	view := ui.NewTester(p.view, 1000, 720)
	if !view.HasText("Opening chat…") || !view.HasText("Settings") {
		t.Fatal("chat startup did not retain its status and settings action")
	}
	for _, text := range []string{"Control Center", "People", "Projects", "Agents"} {
		if view.HasText(text) {
			t.Fatal("chat startup showed the native settings sidebar", text)
		}
	}
}
