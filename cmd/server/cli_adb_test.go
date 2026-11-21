package main

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/adbbridge"
	"spool/internal/workspace"
)

func TestADBBridgeGrantAndSwitchSurviveOtherLocalChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	cliOK(t, root, "user", "add", "--username", "alice", "--adb-bridge=true")
	cliOK(t, root, "user", "add", "--username", "bob", "--remote-desktop=true")
	cliOK(t, root, "-add-user", "carol")
	grants := func() map[string][2]bool {
		t.Helper()
		found := map[string][2]bool{}
		for _, user := range decode[[]workspace.User](t, cliOK(t, root, "user", "list")) {
			found[user.Username] = [2]bool{user.ADBBridge, user.RemoteDesktop}
		}
		return found
	}
	expect := func(why string, alice, bob [2]bool) {
		t.Helper()
		if got := grants(); got["alice"] != alice || got["bob"] != bob || got["carol"] != [2]bool{} {
			t.Fatalf("%s: [adb bridge, remote desktop] grants are %v", why, got)
		}
	}
	expect("new accounts", [2]bool{true, false}, [2]bool{false, true})
	for _, command := range [][]string{
		{"user", "update", "--user", "alice", "--nickname", "Alice"},
		{"user", "disable", "--user", "alice"},
		{"user", "update", "--user", "alice", "--password"},
		{"user", "enable", "--user", "alice"},
		{"user", "update", "--user", "bob", "--username", "bob"},
	} {
		cliOK(t, root, command...)
		expect(strings.Join(command, " "), [2]bool{true, false}, [2]bool{false, true})
	}
	// Each grant changes on its own, through the current and the original flags.
	cliOK(t, root, "user", "update", "--user", "alice", "--remote-desktop=true")
	cliOK(t, root, "user", "update", "--user", "bob", "--adb-bridge=true")
	expect("granting the other access", [2]bool{true, true}, [2]bool{true, true})
	cliOK(t, root, "-remote-desktop-user", "alice", "-remote-desktop=false")
	cliOK(t, root, "user", "update", "--user", "bob", "--adb-bridge=false")
	expect("revoking one access", [2]bool{true, false}, [2]bool{false, true})

	if settings := decode[workspace.Settings](t, cliOK(t, root, "config", "show")); settings.ADBBridgeEnabled {
		t.Fatal("the ADB bridge is on by default")
	}
	settings := decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--adb-bridge=true"))
	if !settings.ADBBridgeEnabled {
		t.Fatal("the ADB bridge was not enabled", settings)
	}
	settings = decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--idle-minutes=42"))
	if !settings.ADBBridgeEnabled || settings.IdleMinutes != 42 {
		t.Fatal("unrelated configuration reset the ADB bridge", settings)
	}
	settings = decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--adb-bridge=false"))
	if settings.ADBBridgeEnabled || settings.IdleMinutes != 42 || settings.RemoteDesktopEnabled {
		t.Fatal("turning the ADB bridge off changed other settings", settings)
	}
}

// The running service must honor what local administration saved. Nothing is
// attached here: that would reach the adb server of whoever runs the tests.
func TestServeADBBridgeAvailability(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	cliOK(t, root, "user", "add", "--username=alice", "--adb-bridge=true")
	cliOK(t, root, "user", "add", "--username=bob")
	cliOK(t, root, "agent", "set", "--backend=codex", "--enabled=false")
	cliOK(t, root, "agent", "set", "--backend=claude", "--enabled=false")
	usable := `{"available":true}`
	if _, installed := adbbridge.Locate(); !installed {
		usable = `{"available":false,"reason":"adb_missing"}`
	}

	for _, mode := range []struct {
		name    string
		options []string
		alice   string
	}{
		{"off by default", nil, `{"available":false,"reason":"disabled"}`},
		{"flag", []string{"--adb-bridge=true"}, usable},
		{"saved", nil, usable},
		{"turned off", []string{"--adb-bridge=false"}, `{"available":false,"reason":"disabled"}`},
	} {
		t.Run(mode.name, func(t *testing.T) {
			origin, stop := startCLITestServer(t, root, mode.options...)
			defer stop()
			client := &http.Client{Timeout: 20 * time.Second}
			request := func(method, path, body, cookie string, want int) (string, []*http.Cookie) {
				t.Helper()
				r, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Origin", origin)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Cookie", cookie)
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != want {
					t.Fatalf("%s %s: status=%d want=%d body=%s err=%v", method, path, response.StatusCode, want, data, err)
				}
				return strings.TrimSpace(string(data)), response.Cookies()
			}
			request("GET", "/api/adb", "", "", http.StatusUnauthorized)
			for username, want := range map[string]string{"alice": mode.alice, "bob": `{"available":false,"reason":"not_authorized"}`} {
				_, cookies := request("POST", "/api/login", fmt.Sprintf(`{"username":%q,"password":"test-password-long-enough"}`, username), "", http.StatusOK)
				if len(cookies) != 1 {
					t.Fatal("login did not return a session")
				}
				cookie := cookies[0].Name + "=" + cookies[0].Value
				if status, _ := request("GET", "/api/adb", "", cookie, http.StatusOK); status != want {
					t.Fatalf("ADB bridge status for %s is %s, want %s", username, status, want)
				}
				if want != usable {
					// An upgrade request that is refused is answered like any other.
					request("GET", "/api/adb/bridge", "", cookie, http.StatusForbidden)
				}
			}
		})
	}
}
