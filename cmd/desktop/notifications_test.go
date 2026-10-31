package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo"
)

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
