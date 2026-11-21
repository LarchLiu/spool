package main

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/workspace"
)

func TestDesktopNotificationCookieWaitLeavesUIAvailable(t *testing.T) {
	var reader desktopNotificationReader
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseLookup := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseLookup)
	posted := make(chan func(), 1)
	applied := make(chan desktopNotificationSession, 1)
	want := desktopNotificationSession{"synthetic-token", "synthetic-account"}

	// Model a UI callback. A slow cookie store must not hold this callback
	// open or start pumping native events inside an active resize operation.
	uiReturned := make(chan struct{})
	go func() {
		reader.read(func() desktopNotificationSession {
			close(started)
			<-release
			return want
		}, func(fn func()) { posted <- fn }, func(session desktopNotificationSession) { applied <- session })
		close(uiReturned)
	}()
	select {
	case <-uiReturned:
	case <-time.After(time.Second):
		releaseLookup()
		<-uiReturned
		t.Fatal("waiting for WebKit cookies held the UI callback open")
	}
	<-started
	if reader.read(func() desktopNotificationSession {
		t.Error("an overlapping poll started a second cookie lookup")
		return desktopNotificationSession{}
	}, func(func()) {}, func(desktopNotificationSession) {}) {
		t.Fatal("a cookie lookup already in progress must coalesce later polls")
	}
	releaseLookup()
	var apply func()
	select {
	case apply = <-posted:
	case <-time.After(time.Second):
		t.Fatal("cookie result was not returned to the UI")
	}
	select {
	case <-applied:
		t.Fatal("the worker applied notification state before UI dispatch")
	default:
	}
	apply()
	if got := <-applied; got != want {
		t.Fatalf("session = %+v, want %+v", got, want)
	}
	if !reader.read(func() desktopNotificationSession { return desktopNotificationSession{} }, func(fn func()) { posted <- fn }, func(desktopNotificationSession) {}) {
		t.Fatal("the next poll was not accepted after the previous one completed")
	}
	select {
	case apply = <-posted:
		apply()
	case <-time.After(time.Second):
		t.Fatal("the next cookie lookup did not complete")
	}
}

func TestDesktopNotificationSessionRequiresOneAuthenticatedHTTPOnlyCookie(t *testing.T) {
	for _, test := range []struct {
		name    string
		cookies []mygo.Cookie
		err     error
		account string
		want    desktopNotificationSession
	}{
		{name: "signed out"},
		{name: "cookie read failed", err: errors.New("unavailable")},
		{name: "renderer cookie", cookies: []mygo.Cookie{{Value: "token"}}, account: "person"},
		{name: "ambiguous sessions", cookies: []mygo.Cookie{{Value: "one", HTTPOnly: true}, {Value: "two", HTTPOnly: true}}, account: "person"},
		{name: "expired session", cookies: []mygo.Cookie{{Value: "token", HTTPOnly: true}}},
		{name: "authenticated", cookies: []mygo.Cookie{{Value: "token", HTTPOnly: true}}, account: "person", want: desktopNotificationSession{"token", "person"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := lookupDesktopNotificationSession("http://127.0.0.1:1234", func(address string) ([]mygo.Cookie, error) {
				if address != "http://127.0.0.1:1234" {
					t.Fatal("cookie lookup changed the service origin")
				}
				return test.cookies, test.err
			}, func(token string) string {
				if len(test.cookies) != 1 || !test.cookies[0].HTTPOnly || token != test.cookies[0].Value {
					t.Fatal("an ambiguous or non-HTTPOnly cookie reached authentication")
				}
				return test.account
			})
			if got != test.want {
				t.Fatalf("session = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestDesktopNotificationReadCompletionDoesNothingAfterQuit(t *testing.T) {
	want := sha256.Sum256([]byte("previous-session"))
	p := &panel{quitting: true, notificationSession: want, notificationClear: func() { t.Fatal("late cookie result changed OS notifications during shutdown") }}
	p.applyDesktopNotificationSession(desktopNotificationSession{"synthetic-token", "synthetic-account"})
	if p.notificationSession != want {
		t.Fatal("late cookie result changed UI state during shutdown")
	}
}

func TestNativeNotificationIDsOnlyContainAccountScopedLocalTargets(t *testing.T) {
	for _, target := range []notificationTarget{
		{"/?chat=chat-12345678", "user-12345678"},
		{"/?inbox=group-12345678&recipient=user-12345678", "user-12345678"},
		{"/?inbox=online-room&recipient=user-12345678", "user-12345678"},
	} {
		id := notificationID(target)
		parsed, ok := notificationTargetFromID(id)
		if !ok || parsed != target || id != notificationID(target) {
			t.Fatalf("native notification did not retain its stable target: %+v %v", parsed, ok)
		}
	}
	for _, target := range []notificationTarget{
		{"https://evil.test/?chat=chat-12345678", "user-12345678"},
		{"//evil.test/?chat=chat-12345678", "user-12345678"},
		{"/?chat=chat-12345678#fragment", "user-12345678"},
		{"/api/me?chat=chat-12345678", "user-12345678"},
		{"/?chat=chat-12345678&chat=other-chat-123", "user-12345678"},
		{"/?chat=chat-12345678&admin=true", "user-12345678"},
		{"/?chat=short", "user-12345678"},
		{"/?chat=chat-12345678", "invalid"},
		{"/?inbox=online-room&recipient=other-user-123", "user-12345678"},
		{"/?inbox=user-12345678&recipient=user-12345678", "user-12345678"},
	} {
		if _, ok := notificationTargetFromID(notificationID(target)); ok {
			t.Errorf("unsafe native notification target accepted: %+v", target)
		}
	}
	for _, id := range []string{"", "spool-notification-test", "spool-native-invalid", "spool-native-e30"} {
		if _, ok := notificationTargetFromID(id); ok {
			t.Fatal("invalid native ID accepted")
		}
	}
}

func TestSystemNotificationTestsHandleSupportAndPermissionFailures(t *testing.T) {
	for _, test := range []struct {
		supported bool
		showErr   error
		want      string
	}{
		{false, nil, "System notifications are unavailable. On macOS, use the packaged desktop app."},
		{true, mygo.ErrNotificationsDenied, "System notifications are blocked. Allow Spool in your system notification settings."},
		{true, errors.New("private native detail"), "Could not show system notification"},
		{true, nil, ""},
	} {
		called := false
		err := showSystemTest(test.supported, mygo.NotificationOptions{Title: "Test"}, func(opts mygo.NotificationOptions) error {
			called = true
			if opts.Title != "Test" {
				t.Fatal("test notification changed options")
			}
			return test.showErr
		})
		if called != test.supported || err == nil && test.want != "" || err != nil && err.Error() != test.want {
			t.Fatalf("system notification error not safely mapped: %v", err)
		}
	}
}

func TestDesktopNotificationFocusDefaultsToNativeAndSupportsIsolatedProbes(t *testing.T) {
	p := &panel{}
	if p.desktopNotificationFocused() {
		t.Fatal("a missing native window must not suppress notifications")
	}
	// Smoke runs must be able to exercise both policies even when a CI desktop
	// refuses foreground activation. No override is installed in normal startup.
	focused := false
	p.notificationFocused = func() bool { return focused }
	for _, focused = range []bool{true, false, true} {
		if got := p.desktopNotificationFocused(); got != focused {
			t.Fatalf("notification focus = %v, want %v", got, focused)
		}
	}
	p.notificationFocused = nil
	if p.desktopNotificationFocused() {
		t.Fatal("clearing the probe override did not restore native focus lookup")
	}
	p.setDesktopNotificationFocused(false) // Safe before a subscription is installed.
}

func TestDesktopNotificationPresentationFollowsViewedSession(t *testing.T) {
	for _, test := range []struct {
		name, tab, viewed, notified string
		read                        string
		focused, loaded, failed     bool
		suppressed                  bool
	}{
		{name: "current foreground session", tab: "Chat", viewed: "chat-current", notified: "chat-current", read: "chat-current", focused: true, loaded: true, suppressed: true},
		{name: "other foreground session", tab: "Chat", viewed: "chat-current", notified: "chat-other", read: "chat-current", focused: true, loaded: true},
		{name: "current background session", tab: "Chat", viewed: "chat-current", notified: "chat-current", loaded: true},
		{name: "settings foreground", tab: "Service", viewed: "chat-current", notified: "chat-current", focused: true, loaded: true},
		{name: "unloaded renderer", tab: "Chat", viewed: "chat-current", notified: "chat-current", focused: true},
		{name: "failed renderer", tab: "Chat", viewed: "chat-current", notified: "chat-current", focused: true, loaded: true, failed: true},
		{name: "foreground message", tab: "Chat", viewed: "chat-current", read: "chat-current", focused: true, loaded: true, suppressed: true},
		{name: "background message", tab: "Chat", viewed: "chat-current", loaded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &panel{tab: test.tab, chatLoaded: test.loaded, chatFailed: test.failed, notificationChat: test.viewed, notificationFocused: func() bool { return test.focused }}
			if got := p.viewedDesktopNotificationChat(test.focused); got != test.read {
				t.Fatalf("read session = %q, want %q", got, test.read)
			}
			for _, kind := range []string{"completed", "failed", "approval"} {
				if got := p.suppressDesktopNotification(workspace.DesktopNotification{ChatID: test.notified, Kind: kind}); got != test.suppressed {
					t.Fatalf("%s suppressed = %v, want %v", kind, got, test.suppressed)
				}
			}
		})
	}
}
