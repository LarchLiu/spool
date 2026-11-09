package relay

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEnrollmentPersistsNameAndSpoolSourceIP(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, name := range []string{"", "家里的 Mac"} {
			t.Run(map[bool]string{false: "token", true: "automatic"}[automatic]+"/"+name, func(t *testing.T) {
				s := testBrokerMode(t, automatic)
				var ip atomic.Value
				ip.Store("198.51.100.12:2345")
				public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					r.RemoteAddr = ip.Load().(string)
					r.Header.Set("X-Forwarded-For", "192.0.2.99")
					s.ServeHTTP(w, r)
				}))
				t.Cleanup(public.Close)
				var token string
				if !automatic {
					record, credential := createViaAPI(t, s, "pre-issued label")
					token = credential
					if record.RegistrationIP != "" {
						t.Fatal("recorded administrator IP before Spool registered")
					}
				}
				id, _ := NewIdentity()
				credential, err := RegisterNamed(t.Context(), public.URL, token, name, id, nil)
				if err != nil {
					t.Fatal(err)
				}
				wantName := name
				if wantName == "" {
					wantName = "pre-issued label"
					if automatic {
						wantName = "Spool-" + id.ID()[:12]
					}
				}
				first := s.registrationStatuses("")[0]
				if first.Name != wantName || first.RegistrationIP != "198.51.100.12" {
					t.Fatal(first)
				}
				ip.Store("203.0.113.9:4321")
				if _, err := Register(t.Context(), public.URL, credential, id, nil); err != nil {
					t.Fatal(err)
				}
				// Ordinary host reconnects cannot rename or overwrite registration IP.
				h, _ := NewHost(t.Context(), public.URL, credential, id, nil)
				c, _, err := h.handshake("/v1/host", "")
				if err != nil {
					t.Fatal(err)
				}
				c.CloseNow()
				h.Close()
				if got := s.registrationStatuses("")[0]; got.Name != wantName || got.RegistrationIP != first.RegistrationIP {
					t.Fatal(got)
				}
				s.Close()
				restored, err := NewServer(s.opts)
				if err != nil {
					t.Fatal(err)
				}
				defer restored.Close()
				response := adminRequest(t, restored, "GET", "/v1/registrations/"+first.ID, testAdminToken, "")
				var metadata Registration
				if json.Unmarshal(response.Body.Bytes(), &metadata) != nil || metadata.Name != wantName || metadata.RegistrationIP != first.RegistrationIP {
					t.Fatal(response.Body)
				}
			})
		}
	}
}

func TestInvalidEnrollmentNameDoesNotClaimToken(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	defer public.Close()
	record, token := createViaAPI(t, s, "home")
	id, _ := NewIdentity()
	for _, name := range []string{"bad\x00name", strings.Repeat("家", 43)} {
		if _, err := RegisterNamed(t.Context(), public.URL, token, name, id, nil); !errors.Is(err, ErrInvalidRegistration) {
			t.Fatal("invalid name accepted", err)
		}
		// Bypass client validation and exercise the authenticated server parser.
		h, _ := NewHost(t.Context(), public.URL, token, id, nil)
		if c, _, err := h.handshake("/v1/register", name); err == nil {
			c.CloseNow()
			t.Fatal("server accepted invalid name")
		}
		h.Close()
	}
	if got := s.registrationStatuses(record.ID)[0]; got.ServerID != "" || got.RegistrationIP != "" || got.Name != "home" {
		t.Fatal("failed registration mutated metadata", got)
	}
	// Name changes are covered by the signature, not unauthenticated fields.
	c, err := dialWebsocket(t.Context(), nil, public.URL, "/v1/register", token)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	challenge, _ := readControl(t.Context(), c)
	writeControl(t.Context(), c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Name: "tampered", Signature: ed25519.Sign(id.PrivateKey(), proof(challenge.Challenge, id.AccessToken))})
	if m, err := readControl(t.Context(), c); err == nil && m.Type == "ready" {
		t.Fatal("unsigned name accepted")
	}
}

func TestFirstHostConnectionRecordsSpoolIP(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "token", true: "automatic"}[automatic], func(t *testing.T) {
			s := testBrokerMode(t, automatic)
			public := httptest.NewServer(s)
			defer public.Close()
			var token string
			if !automatic {
				_, token = createViaAPI(t, s, "older Spool client")
			}
			id, _ := NewIdentity()
			h, err := NewHost(t.Context(), public.URL, token, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			c, _, err := h.handshake("/v1/host", "")
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			if got := s.registrationStatuses("")[0]; got.RegistrationIP != "127.0.0.1" || got.ServerID != id.ID() {
				t.Fatal("initial host enrollment did not record the Spool source", got)
			}
		})
	}
}

func TestRegistrationIPBehindTrustedProxyAndLegacyUpgrade(t *testing.T) {
	s := testBroker(t)
	registration, token := createViaAPI(t, s, "existing server")
	id, _ := NewIdentity()
	record := s.registrations[registration.ID]
	record.ServerID = id.ID()
	// An older relay saved the identity but had no registration IP field.
	data, err := json.Marshal(struct {
		Version       int                  `json:"version"`
		Registrations []registrationRecord `json:"registrations"`
	}{1, []registrationRecord{record}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.WriteFile(filepath.Join(s.opts.DataDir, "registrations.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	opts := s.opts
	opts.TrustedProxies, err = ParseTrustedProxies("10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "10.0.0.2:1234"
		// A client-provided prefix must not override the first untrusted hop.
		r.Header.Set("X-Forwarded-For", "192.0.2.99, 198.51.100.12, 10.0.0.3")
		upgraded.ServeHTTP(w, r)
	}))
	defer public.Close()
	h, _ := NewHost(t.Context(), public.URL, token, id, nil)
	c, _, err := h.handshake("/v1/host", "")
	if err != nil {
		h.Close()
		t.Fatal(err)
	}
	c.CloseNow()
	h.Close()
	if got := upgraded.registrationStatuses(registration.ID)[0]; got.RegistrationIP != "" {
		t.Fatal("reconnection invented a historical registration IP", got)
	}
	if _, err := Register(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	response := adminRequest(t, upgraded, "GET", "/v1/registrations", testAdminToken, "")
	var result struct {
		Registrations []Registration `json:"registrations"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil ||
		len(result.Registrations) != 1 || result.Registrations[0].RegistrationIP != "198.51.100.12" {
		t.Fatal("management list did not return Spool's forwarded source IP", response.Body)
	}
	upgraded.Close()
	restored, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.registrationStatuses(registration.ID)[0]; got.RegistrationIP != "198.51.100.12" {
		t.Fatal("registration IP did not survive the store upgrade", got)
	}
}
