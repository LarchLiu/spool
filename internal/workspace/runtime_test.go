package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoopbackAddressUsesMatchingIPFamily(t *testing.T) {
	for address, want := range map[string]string{
		"127.0.0.1:8787": "127.0.0.1:8787",
		"127.0.0.2:1234": "127.0.0.2:1234",
		"0.0.0.0:8787":   "127.0.0.1:8787",
		"[::]:8787":      "[::1]:8787",
		"[::1]:8787":     "[::1]:8787",
		"192.0.2.1:8787": "",
		"localhost:8787": "",
		"invalid":        "",
	} {
		if got := loopbackAddress(address); got != want {
			t.Errorf("loopbackAddress(%q) = %q, want %q", address, got, want)
		}
	}
}

func TestLocalWebURLUsesLiveListenerNotSavedSettingsOrProxy(t *testing.T) {
	store := testStore(t)
	settings := DefaultSettings()
	settings.PublicURL = "https://chat.example.test"
	settings.Port = 9999
	if err := store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}
	if _, err := service.LocalWebURL(); err == nil {
		t.Fatal("stopped service provided a desktop URL")
	}
	service.server = &http.Server{}
	service.localAddress = "[::1]:12345"
	if got, err := service.LocalWebURL(); err != nil || got != "http://[::1]:12345" {
		t.Fatalf("local URL = %q, %v", got, err)
	}
	if got := service.WebURL(); got != settings.PublicURL {
		t.Fatalf("browser URL no longer uses the configured proxy: %q", got)
	}
}

func TestDesktopEndpointSharesServiceAuthenticationAndLifecycle(t *testing.T) {
	hosts := []string{"127.0.0.1", "0.0.0.0", "::"}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() && network.IP.IsGlobalUnicast() {
				hosts = append(hosts, network.IP.String())
				break
			}
		}
	}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("listener unavailable: %v", err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			root := t.TempDir()
			t.Setenv("SPOOL_CODEX_BIN", filepath.Join(root, "missing-codex"))
			t.Setenv("SPOOL_CLAUDE_BIN", filepath.Join(root, "missing-claude"))
			service, err := OpenService(root)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			user := testUser(t, service.Store, "desktop")
			settings := DefaultSettings()
			settings.Host, settings.Port, settings.PublicURL = host, port, "https://chat.example.test"
			if err := service.Store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.Start(); err != nil {
				t.Fatal(err)
			}
			address, err := service.LocalWebURL()
			if err != nil || !strings.HasPrefix(address, "http://") {
				t.Fatalf("desktop URL %q, %v", address, err)
			}
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			request := func(method, path, body, csrf string, want int) []byte {
				t.Helper()
				r, err := http.NewRequest(method, address+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if method == "POST" {
					r.Header.Set("Origin", address)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("X-CSRF-Token", csrf)
				}
				res, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				data, _ := io.ReadAll(res.Body)
				if res.StatusCode != want {
					t.Fatalf("%s %s = %d, want %d: %s", method, path, res.StatusCode, want, data)
				}
				return data
			}
			request("GET", "/api/me", "", "", 401)
			request("POST", "/api/login", fmt.Sprintf(`{"username":%q,"password":"test-password-2026"}`, user.Username), "", 200)
			var me struct {
				User User   `json:"user"`
				CSRF string `json:"csrf"`
			}
			if err := json.Unmarshal(request("GET", "/api/me", "", "", 200), &me); err != nil || me.User.ID != user.ID || me.CSRF == "" {
				t.Fatalf("desktop login did not keep its Cookie/CSRF identity: %+v (%v)", me, err)
			}
			request("POST", "/api/preferences", `{"theme":"dark","language":"en"}`, "", 403)
			request("POST", "/api/preferences", `{"theme":"dark","language":"en"}`, me.CSRF, 200)
			// Saving unapplied settings must not change the active desktop URL.
			settings.Port = port%65535 + 1
			if err := service.Store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if got, err := service.LocalWebURL(); err != nil || got != address {
				t.Fatalf("saved settings changed active endpoint to %q (%v)", got, err)
			}
			if err := service.Stop(); err != nil {
				t.Fatal(err)
			}
			if _, err := service.LocalWebURL(); err == nil {
				t.Fatal("stopped service still exposes a desktop endpoint")
			}
			if res, err := client.Get(address + "/healthz"); err == nil {
				res.Body.Close()
				t.Fatal("stopping the service left its desktop listener open")
			}
			settings.Port = port
			if err := service.Store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.Start(); err != nil {
				t.Fatal(err)
			}
			address, err = service.LocalWebURL()
			if err != nil {
				t.Fatal(err)
			}
			request("GET", "/api/me", "", "", 200)
		})
	}
}

func TestDefaultDataDir(t *testing.T) {
	for _, development := range []bool{false, true} {
		name := ".spool"
		if development {
			name = ".spool-dev"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPOOL_HOME", "")
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := DefaultDataDir(development), filepath.Join(home, name); got != want {
				t.Fatalf("DefaultDataDir(%v) = %q, want %q", development, got, want)
			}
			root := t.TempDir()
			t.Setenv("SPOOL_HOME", "  "+root+"  ")
			if got := DefaultDataDir(development); got != root {
				t.Fatalf("DefaultDataDir(%v) = %q, want %q", development, got, root)
			}
		})
	}
}
