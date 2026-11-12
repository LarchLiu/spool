package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/workspace"
)

func TestUserRemoteDesktopGrantsPreserveOtherFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	alice := decode[workspace.User](t, cliOK(t, root, "user", "add", "--username", "alice", "--remote-desktop=true"))
	bob := decode[workspace.User](t, cliOK(t, root, "user", "add", "--username", "bob"))
	if !alice.RemoteDesktop || bob.RemoteDesktop {
		t.Fatalf("unexpected initial grants: alice=%+v bob=%+v", alice, bob)
	}
	for _, args := range [][]string{
		{"update", "--user", "alice", "--nickname", "Alice"},
		{"disable", "--user", "alice"},
		{"update", "--user", "alice", "--password"},
		{"enable", "--user", "alice"},
	} {
		u := decode[workspace.User](t, cliOK(t, root, append([]string{"user"}, args...)...))
		if !u.RemoteDesktop || u.Nickname != "Alice" {
			t.Fatal("account edit discarded the remote desktop grant or nickname", u)
		}
	}
	alice = decode[workspace.User](t, cliOK(t, root, "user", "update", "--user", alice.ID, "--remote-desktop=false"))
	bob = decode[workspace.User](t, cliOK(t, root, "user", "update", "--user", "BOB", "--remote-desktop=true"))
	if alice.RemoteDesktop || alice.Disabled || alice.Nickname != "Alice" || !bob.RemoteDesktop {
		t.Fatalf("grant update lost account fields: alice=%+v bob=%+v", alice, bob)
	}
}

func installCLIVoxlFixture(t *testing.T, root string) localVoxl {
	t.Helper()
	t.Setenv("SPOOL_TEST_VOXL_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return decode[localVoxl](t, cliOK(t, root, "voxl", "install", "--binary", binary))
}

func TestVoxlInstallationAndConfigDoNotStartCapture(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	marker := filepath.Join(t.TempDir(), "capture-started")
	t.Setenv("SPOOL_TEST_VOXL_STARTED", marker)
	status := decode[localVoxl](t, cliOK(t, root, "voxl", "status"))
	if status.Installed || status.Enabled || status.Running {
		t.Fatal("unexpected uninstalled status", status)
	}
	// The data store resolves symlinks, including macOS's /var alias.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolvedRoot, "voxl"); status.Directory != want {
		t.Fatalf("helper directory: got %q, want %q", status.Directory, want)
	}
	before := decode[workspace.Settings](t, cliOK(t, root, "config", "show"))
	for _, command := range [][]string{
		{"config", "set", "--remote-desktop=true", "--idle-minutes=42"},
		{"serve", "--remote-desktop=true", "--idle-minutes=42"},
	} {
		if _, err := invoke(t, root, command...); err == nil || !strings.Contains(err.Error(), "voxl install") {
			t.Fatal("enabled remote desktop without a helper", command, err)
		}
		settings := decode[workspace.Settings](t, cliOK(t, root, "config", "show"))
		if settings.RemoteDesktopEnabled || settings.IdleMinutes != before.IdleMinutes {
			t.Fatal("failed configuration changed saved settings", settings)
		}
	}
	status = installCLIVoxlFixture(t, root)
	if !status.Installed || status.Running || status.Enabled {
		t.Fatal("install did not prepare an idle helper", status)
	}
	settings := decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--remote-desktop=true"))
	if !settings.RemoteDesktopEnabled {
		t.Fatal("remote desktop was not enabled", settings)
	}
	settings = decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--idle-minutes=42"))
	if !settings.RemoteDesktopEnabled || settings.IdleMinutes != 42 {
		t.Fatal("unrelated configuration reset remote desktop", settings)
	}
	status = decode[localVoxl](t, cliOK(t, root, "voxl", "status"))
	if !status.Enabled || status.Running {
		t.Fatal("saved enablement not reflected in offline status", status)
	}
	settings = decode[workspace.Settings](t, cliOK(t, root, "config", "set", "--remote-desktop=false"))
	if settings.RemoteDesktopEnabled || settings.IdleMinutes != 42 {
		t.Fatal("explicit disable lost unrelated settings", settings)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("local administration started desktop capture", err)
	}
}

func startCLITestServer(t *testing.T, root string, options ...string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var out, stderr bytes.Buffer
	var serveErr error
	done := make(chan struct{})
	args := append([]string{"--data-dir", root, "serve", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port)}, options...)
	go func() {
		serveErr = runCLI(ctx, args, &out, &stderr)
		close(done)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("serve: %v\n%s\n%s", serveErr, &out, &stderr)
			}
		case <-time.After(20 * time.Second):
			t.Error("serve did not stop")
		}
	}
	t.Cleanup(stop)
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("serve exited before readiness: %v\n%s\n%s", serveErr, &out, &stderr)
		default:
		}
		response, err := client.Get(origin + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return origin, stop
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serve did not become ready")
	return "", stop
}

func TestServeRemoteDesktopChannel(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	marker := filepath.Join(t.TempDir(), "capture-started")
	t.Setenv("SPOOL_TEST_VOXL_STARTED", marker)
	installCLIVoxlFixture(t, root)
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	cliOK(t, root, "user", "add", "--username=alice", "--remote-desktop=true")
	cliOK(t, root, "user", "add", "--username=bob")
	cliOK(t, root, "agent", "set", "--backend=codex", "--enabled=false")
	cliOK(t, root, "agent", "set", "--backend=claude", "--enabled=false")

	for _, mode := range []string{"flag", "saved", "disabled", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			var options []string
			switch mode {
			case "flag":
				options = []string{"--remote-desktop=true"}
			case "disabled":
				options = []string{"--remote-desktop=false"}
			case "revoked":
				cliOK(t, root, "user", "update", "--user=alice", "--remote-desktop=false")
				cliOK(t, root, "config", "set", "--remote-desktop=true")
			}
			origin, stop := startCLITestServer(t, root, options...)
			client := &http.Client{Timeout: 20 * time.Second}
			type result struct {
				body    string
				cookies []*http.Cookie
			}
			request := func(method, path, body, cookie, csrf string, want int) result {
				t.Helper()
				r, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Origin", origin)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Cookie", cookie)
				r.Header.Set("X-CSRF-Token", csrf)
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != want {
					t.Fatalf("%s %s: status=%d want=%d body=%s err=%v", method, path, response.StatusCode, want, data, err)
				}
				return result{body: string(data), cookies: response.Cookies()}
			}
			request("GET", "/api/desktop", "", "", "", http.StatusUnauthorized)
			for _, username := range []string{"bob", "alice"} {
				response := request("POST", "/api/login", fmt.Sprintf(`{"username":%q,"password":"test-password-long-enough"}`, username), "", "", http.StatusOK)
				login := decode[struct {
					CSRF string `json:"csrf"`
				}](t, response.body)
				if len(response.cookies) != 1 || login.CSRF == "" {
					t.Fatal("login did not return session credentials")
				}
				cookie := response.cookies[0].Name + "=" + response.cookies[0].Value
				available := username == "alice" && (mode == "flag" || mode == "saved")
				status := decode[struct{ Available bool }](t, request("GET", "/api/desktop", "", cookie, "", http.StatusOK).body)
				if status.Available != available {
					t.Fatalf("desktop availability for %s: got=%v want=%v", username, status.Available, available)
				}
				if !available {
					request("POST", "/api/desktop/connect", `{}`, cookie, login.CSRF, http.StatusForbidden)
					continue
				}
				view := decode[struct{ Token string }](t, request("POST", "/api/desktop/connect", `{}`, cookie, login.CSRF, http.StatusOK).body)
				if mode == "flag" {
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("starting the service or creating a view started capture", err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				url := "ws" + strings.TrimPrefix(origin, "http") + "/api/desktop/view/ws?token=" + view.Token
				conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie}, "Origin": {origin}}})
				if err != nil {
					t.Fatal("connect desktop through server CLI", err)
				}
				defer conn.CloseNow()
				if err := conn.Write(ctx, websocket.MessageText, []byte("desktop channel")); err != nil {
					t.Fatal(err)
				}
				_, data, err := conn.Read(ctx)
				if err != nil || string(data) != "desktop channel" {
					t.Fatal("desktop channel did not forward data", string(data), err)
				}
				stop()
				if _, _, err := conn.Read(ctx); err == nil {
					t.Fatal("desktop channel survived server shutdown")
				}
			}
			stop()
			status := decode[localVoxl](t, cliOK(t, root, "voxl", "status"))
			if !status.Installed || status.Running || status.Enabled != (mode != "disabled") {
				t.Fatal("serve did not persist enablement or stop the helper", status)
			}
		})
	}
}
