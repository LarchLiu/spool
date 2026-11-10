package spoolnet

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerSessionAddressChangeDoesNotAuthorizeNewOrigin(t *testing.T) {
	for _, directMode := range []bool{true, false} {
		t.Run(map[bool]string{true: "public", false: "tailscale"}[directMode], func(t *testing.T) {
			for _, change := range []string{"host", "port"} {
				t.Run(change, func(t *testing.T) {
					root := t.TempDir()
					direct, tail := "https://public.example.com", "https://private.example.ts.net"
					address := direct
					if !directMode {
						address = tail
					}
					open := func(endpoint, public, tailscale string) *Client {
						t.Helper()
						client, err := NewDirectClient(filepath.Join(root, "transport"), endpoint)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { client.Close() })
						client.direct = directMode
						if err := client.UseServerSession(filepath.Join(root, "session"), public, tailscale); err != nil {
							t.Fatal(err)
						}
						return client
					}
					original := open(address, direct, tail)
					original.http.Jar.SetCookies(original.origin, []*http.Cookie{sessionCookie("original-login")})
					original.Close()
					replacement := "https://replacement.example.com"
					if change == "port" {
						replacement = address + ":8443"
					}
					if directMode {
						direct = replacement
					} else {
						tail = replacement
					}
					changed := open(replacement, direct, tail)
					requireSession(t, changed.http.Jar, changed.origin, "")
					changed.Close()
					if directMode {
						direct = address
					} else {
						tail = address
					}
					restored := open(address, direct, tail)
					requireSession(t, restored.http.Jar, restored.origin, "")
				})
			}
		})
	}
}

func TestServerSessionModesAreIndependentEvenAtTheSameOrigin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "session")
	address := "https://public.example.com"
	origin := sessionOrigin(t, address)
	for _, mode := range []string{"https", "tsnet", "relay"} {
		jar, err := newServerSessionJar(root, origin, address, address, mode, address)
		if err != nil {
			t.Fatal(err)
		}
		requireSession(t, jar, origin, "")
		jar.SetCookies(origin, []*http.Cookie{sessionCookie(mode + "-login")})
		jar.close()
	}
	for _, mode := range []string{"https", "tsnet", "relay"} {
		jar, err := newServerSessionJar(root, origin, address, address, mode, address)
		if err != nil {
			t.Fatal(err)
		}
		requireSession(t, jar, origin, mode+"-login")
		jar.close()
	}
}

func TestServerSessionReplacementRejectsLoginAcrossAPIAndStreams(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "session")
	previous, tail := "https://previous.example.com", "https://private.example.ts.net"
	jar, err := newServerSessionJar(directory, sessionOrigin(t, previous), previous, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(jar.origin, []*http.Cookie{sessionCookie("previous-login")})
	jar.close()
	requests := make(chan string, 4)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("replacement origin received a previous route's login")
		}
		requests <- r.URL.Path
		if r.URL.Path == "/healthz" {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		http.Error(w, "sign in required", http.StatusUnauthorized)
	})
	client.direct = true
	if err := client.UseServerSession(directory, client.origin.String(), tail); err != nil {
		t.Fatal(err)
	}
	if err := client.probeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Request("GET", "/api/me", "", "", "en")
	var response apiResponse
	if err != nil || json.Unmarshal([]byte(raw), &response) != nil || response.Status != 401 {
		t.Fatal("replacement unexpectedly restored an account", err)
	}
	for _, workspace := range []bool{false, true} {
		events := make(eventRecorder, 1)
		var stream *EventStream
		if workspace {
			stream, err = client.SubscribeWorkspace("fixture-csrf", "en", events)
		} else {
			stream, err = client.SubscribeChat("chat", "en", events)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stream.Close)
		select {
		case event := <-events:
			if event != [2]string{"transport_error", `{"status":401}`} {
				t.Fatal("replacement stream unexpectedly authenticated", event)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("replacement stream did not finish its authentication check")
		}
		stream.Close()
	}
	for _, want := range []string{"/healthz", "/api/me", "/api/chats/chat/events", "/api/events"} {
		if got := <-requests; got != want {
			t.Fatalf("unexpected request path: got %s, want %s", got, want)
		}
	}
}

func TestServerSessionRelayIdentityReplacementAndRemovedRoutesDoNotReviveLogin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "session")
	direct := "https://public.example.com"
	first, next := "https://first.spool.invalid", "https://next.spool.invalid"
	open := func(mode, address, relayOrigin string) *serverSessionJar {
		t.Helper()
		jar, err := newServerSessionJar(root, sessionOrigin(t, address), direct, "", mode, relayOrigin)
		if err != nil {
			t.Fatal(err)
		}
		return jar
	}
	jar := open("relay", first, first)
	jar.SetCookies(jar.origin, []*http.Cookie{sessionCookie("first-relay-login")})
	jar.close()
	// Keeping the Public route does not authorize a different paired identity.
	jar = open("relay", next, next)
	requireSession(t, jar, jar.origin, "")
	jar.SetCookies(jar.origin, []*http.Cookie{sessionCookie("next-relay-login")})
	jar.close()
	jar = open("https", direct, next)
	requireSession(t, jar, jar.origin, "")
	jar.SetCookies(jar.origin, []*http.Cookie{sessionCookie("public-login")})
	jar.close()
	jar = open("relay", next, next)
	requireSession(t, jar, jar.origin, "next-relay-login")
	jar.close()
	// Removing a route prunes its login; re-adding it cannot restore an old file.
	jar = open("https", direct, "")
	requireSession(t, jar, jar.origin, "public-login")
	jar.close()
	jar = open("relay", next, next)
	requireSession(t, jar, jar.origin, "")
	jar.close()
}

func TestServerSessionUpgradeRejectsUnattributedSharedCookies(t *testing.T) {
	for _, kind := range []string{"shared", "malformed", "unsupported-version"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "session")
			direct, tail, paired := "https://public.example.com", "https://private.example.ts.net", "https://paired.spool.invalid"
			for mode, address := range map[string]string{"https": direct, "tsnet": tail, "relay": paired} {
				savedLegacyLogin(t, root, mode, address, "stale-legacy-login")
			}
			cookie := sessionCookie("unattributed-shared-login")
			cookie.Expires = time.Now().Add(time.Hour)
			data, err := json.Marshal(struct {
				Origins []string     `json:"origins"`
				Cookie  *http.Cookie `json:"cookie"`
			}{[]string{direct, tail, paired}, cookie})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "malformed":
				data = []byte("invalid")
			case "unsupported-version":
				data = []byte(`{"version":99,"sessions":{}}`)
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "session.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			for mode, address := range map[string]string{"https": direct, "tsnet": tail, "relay": paired} {
				jar, err := newServerSessionJar(directory, sessionOrigin(t, address), direct, tail, mode, paired)
				if err != nil {
					t.Fatal(err)
				}
				requireSession(t, jar, jar.origin, "")
				jar.close()
			}
		})
	}
}

func TestProbeDoesNotSendOrAcceptSessionCookies(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("health check disclosed authentication credentials")
		}
		http.SetCookie(w, sessionCookie("health-response-login"))
		io.WriteString(w, `{"ok":true}`)
	})
	client.http.Jar.SetCookies(client.origin, []*http.Cookie{sessionCookie("existing-login")})
	if err := client.probeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireSession(t, client.http.Jar, client.origin, "existing-login")
}
