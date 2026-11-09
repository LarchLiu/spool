package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"spool/relay"
)

func TestRelayUnregisterPreservesOtherOriginsAndStopsOnlyRelay(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	var urls, tokens []string
	var reject atomic.Bool
	for range 2 {
		broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), AllowUnregisteredHosts: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { broker.Close() })
		public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reject.Load() && r.URL.Path == "/v1/unregister" {
				http.Error(w, "offline", 503)
				return
			}
			broker.ServeHTTP(w, r)
		}))
		t.Cleanup(public.Close)
		settings.RelayURL = public.URL
		if err := service.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if err := service.RegisterRelayNamed(t.Context(), public.URL, "", "Home Mac"); err != nil {
			t.Fatal(err)
		}
		urls = append(urls, public.URL)
		tokens = append(tokens, service.relayRegistrationToken(public.URL))
	}
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings.RelayLANPort = port.Addr().(*net.TCPAddr).Port
	port.Close()
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.Relay.State == "connected" })
	reject.Store(true)
	if err := service.UnregisterRelay(t.Context(), urls[1]); err == nil {
		t.Fatal("failed remote unregister reported success")
	}
	if !service.Status().Relay.Registered || service.relayRegistrationToken(urls[1]) != tokens[1] {
		t.Fatal("failed unregister erased credential")
	}
	reject.Store(false)
	if err := service.UnregisterRelay(t.Context(), urls[1]); err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	settings, _ = service.Store.Settings()
	if !status.Running || status.RestartRequired || status.Relay.Registered || status.Relay.RegistrationTokenSet || status.Relay.Registering || settings.RelayEnabled || service.relay != nil {
		t.Fatal("unregister did not disable only relay", status)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	address, _ := service.LocalWebURL()
	response, err := client.Get(address + "/healthz")
	if err != nil {
		t.Fatal("local Spool service stopped", err)
	}
	response.Body.Close()
	root := service.Store.Root()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if service.Status().Relay.Registered || service.relayRegistrationToken(urls[1]) != "" {
		t.Fatal("removed credential returned after restart")
	}
	settings.RelayURL = urls[0]
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !service.Status().Relay.Registered || service.relayRegistrationToken(urls[0]) != tokens[0] {
		t.Fatal("other relay credential was removed")
	}
	settings.RelayURL = urls[1]
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), urls[1], ""); err != nil {
		t.Fatal("could not register again after self-unregistration", err)
	}
}

func TestRelayRegistrationsRememberEachOriginAcrossRestarts(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "required-token"
		if automatic {
			name = "automatic-token"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
			service, settings := settingsService(t)
			type endpoint struct {
				url, token                 string
				registrations, connections atomic.Int32
				wrongToken                 atomic.Bool
			}
			var endpoints []*endpoint
			for range 2 {
				broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), AllowUnregisteredHosts: automatic})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { broker.Close() })
				e := &endpoint{}
				if !automatic {
					_, e.token, err = broker.CreateRegistration("remembered relay")
					if err != nil {
						t.Fatal(err)
					}
				}
				public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/register" {
						e.registrations.Add(1)
					}
					if r.URL.Path == "/v1/host" {
						e.connections.Add(1)
						if r.Header.Get("Authorization") != "Bearer "+e.token {
							e.wrongToken.Store(true)
						}
					}
					broker.ServeHTTP(w, r)
				}))
				t.Cleanup(public.Close)
				e.url = public.URL
				settings.RelayURL = e.url
				if err := service.SaveSettings(settings); err != nil {
					t.Fatal(err)
				}
				if status := service.Status().Relay; status.Registered || status.RegistrationTokenSet {
					t.Fatal("another origin's credential or enrollment was reused", status)
				}
				if err := service.RegisterRelay(t.Context(), e.url, e.token); err != nil {
					t.Fatal(err)
				}
				e.token = service.relayRegistrationToken(e.url)
				if e.token == "" || !service.Status().Relay.Registered {
					t.Fatal("registration did not save its credential")
				}
				endpoints = append(endpoints, e)
			}
			if endpoints[0].token == endpoints[1].token {
				t.Fatal("relays must retain their independently issued credentials")
			}
			root := service.Store.Root()
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			service, err = OpenService(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Close() })
			port, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			settings.RelayLANPort = port.Addr().(*net.TCPAddr).Port
			port.Close()
			for _, e := range []*endpoint{endpoints[0], endpoints[1], endpoints[0]} {
				settings.RelayEnabled = false
				settings.RelayURL = e.url + "/"
				if err := service.SaveSettings(settings); err != nil {
					t.Fatal(err)
				}
				status := service.Status().Relay
				if !status.Registered || !status.RegistrationTokenSet || service.relayRegistrationToken(settings.RelayURL) != e.token {
					t.Fatal("returning to a relay did not restore its registration", status)
				}
				// Even an explicit repeated register action must not rotate an
				// automatically issued token or contact the enrollment endpoint.
				if err := service.RegisterRelay(t.Context(), settings.RelayURL, ""); err != nil {
					t.Fatal(err)
				}
				settings.RelayEnabled = true
				if err := service.SaveSettings(settings); err != nil {
					t.Fatal("remembered registration did not unlock relay", err)
				}
				if err := service.Start(); err != nil {
					t.Fatal(err)
				}
				waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
				if err := service.Stop(); err != nil {
					t.Fatal(err)
				}
			}
			for _, e := range endpoints {
				if e.registrations.Load() != 1 || e.connections.Load() == 0 || e.wrongToken.Load() {
					t.Fatal("switching relays repeated enrollment or connected with the wrong token")
				}
			}
		})
	}
}

func TestRelayRegistrationMigratesLegacyCredentialForItsOriginalOrigin(t *testing.T) {
	for _, changeToken := range []bool{false, true} {
		name := "confirmed"
		if changeToken {
			name = "unconfirmed replacement"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
			root := t.TempDir()
			store, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}
			settings := DefaultSettings()
			settings.RelayURL = "https://second-relay.example.com"
			if err := store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			store.Close()
			id, err := relay.LoadIdentity(filepath.Join(root, "relay"))
			if err != nil {
				t.Fatal(err)
			}
			token := strings.Repeat("a", 43)
			hash := sha256.Sum256([]byte(token))
			legacy, err := json.Marshal(map[string]string{"url": "https://FIRST-relay.example.com:443/", "server_id": id.ID(), "token_hash": hex.EncodeToString(hash[:])})
			if err != nil {
				t.Fatal(err)
			}
			if changeToken {
				token = strings.Repeat("b", 43)
			}
			for name, body := range map[string][]byte{"registration.json": legacy, "registration-token": []byte(token)} {
				if err := os.WriteFile(filepath.Join(root, "relay", name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			service, err := OpenService(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Close() })
			if status := service.Status().Relay; status.Registered || status.RegistrationTokenSet {
				t.Fatal("legacy token was assigned to the newly selected origin", status)
			}
			settings.RelayURL = "https://first-relay.example.com"
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if status := service.Status().Relay; status.Registered == changeToken || !status.RegistrationTokenSet || service.relayRegistrationToken(settings.RelayURL) != token {
				t.Fatal("migration lost the original origin or trusted a changed token", status)
			}
			info, err := os.Stat(filepath.Join(root, "relay", "registrations.json"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("migrated credentials are not private", err)
			}
			service.mu.Lock()
			err = service.invalidateRelayRegistrationLocked(settings.RelayURL)
			service.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			service, err = OpenService(root)
			if err != nil {
				t.Fatal(err)
			}
			if service.Status().Relay.Registered {
				t.Fatal("stale legacy confirmation restored invalidated registration")
			}
		})
	}
}

func TestRelayCredentialsScopeEnvironmentTokenToStartupOrigin(t *testing.T) {
	const first = "https://first-relay.example.com"
	const second = "https://second-relay.example.com"
	token := strings.Repeat("e", 43)
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", token)
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	settings.RelayURL = first
	if err := store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	store.Close()
	service, err := OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if service.relayRegistrationToken(first) != token || service.Status().Relay.Registered {
		t.Fatal("environment credential must be imported without confirming enrollment")
	}
	settings.RelayURL = second
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if service.Status().Relay.RegistrationTokenSet || service.relayRegistrationToken(second) != "" {
		t.Fatal("environment credential leaked to another relay")
	}
}

func TestRelayOriginUsesSchemeHostnameAndEffectivePort(t *testing.T) {
	for input, want := range map[string]string{
		" https://Relay.Example.com:443/ ":      "https://relay.example.com",
		"https://relay.example.com:28080":       "https://relay.example.com:28080",
		"http://127.0.0.1:80/":                  "http://127.0.0.1",
		"https://[::1]:443/":                    "https://[::1]",
		"https://[::1]:28080":                   "https://[::1]:28080",
		"https://relay.example.com/v1/register": "",
	} {
		if got := relayOrigin(input); got != want {
			t.Errorf("origin of %q = %q; want %q", input, got, want)
		}
	}
}
