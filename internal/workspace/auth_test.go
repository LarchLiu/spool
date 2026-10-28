package workspace

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHTTPSProxyKeepsSecureCookiesExceptDirectLoopback(t *testing.T) {
	a := &App{settings: Settings{PublicURL: "https://chat.example.test"}}
	for _, tc := range []struct {
		host, peer  string
		tls, secure bool
	}{
		{"127.0.0.1:8787", "127.0.0.1:50000", false, false},
		{"localhost:8787", "127.0.0.1:50000", false, false},
		{"[::1]:8787", "[::1]:50000", false, false},
		{"127.0.0.1:8787", "127.0.0.1:50000", true, true},
		{"chat.example.test", "127.0.0.1:50000", false, true},
		{"127.0.0.1:8787", "192.0.2.1:50000", false, true},
		{"localhost.evil.example:8787", "127.0.0.1:50000", false, true},
		{"192.0.2.1:8787", "127.0.0.1:50000", false, true},
		{"localhost:8787", "invalid", false, true},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/login", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("Origin", "http://"+tc.host)
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := a.secureCookie(r); got != tc.secure {
			t.Errorf("host=%s peer=%s tls=%v: Secure=%v, want %v", tc.host, tc.peer, tc.tls, got, tc.secure)
		}
	}
	// Reverse proxies may rewrite Host to the loopback upstream. A public
	// browser's origin (or an absent one) must never downgrade its cookie.
	for _, origin := range []string{"", "https://chat.example.test"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/login", nil)
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Origin", origin)
		if !a.secureCookie(r) {
			t.Errorf("rewritten proxy Host with origin %q lost Secure", origin)
		}
	}
}

func TestUserFixturesUseMinimumBcryptCost(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "fixture")
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcrypt.MinCost {
		t.Fatalf("fixture bcrypt cost = %d, want %d: %v", cost, bcrypt.MinCost, err)
	}
	if _, _, err := s.login(u.Username, "test-password-2026"); err != nil {
		t.Fatal("fixture cannot authenticate:", err)
	}
}

func TestSaveUserUsesProductionBcryptCost(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "create"
		if update {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			input := UserInput{Username: "alice", Nickname: "Alice", Password: "production-password-2026"}
			var oldToken string
			if update {
				u := testUser(t, s, input.Username)
				input.ID = u.ID
				var err error
				oldToken, _, err = s.login(u.Username, "test-password-2026")
				if err != nil {
					t.Fatal(err)
				}
			}
			u, err := s.SaveUser(input)
			if err != nil {
				t.Fatal(err)
			}
			var hash string
			if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != 12 {
				t.Fatalf("production bcrypt cost = %d, want 12: %v", cost, err)
			}
			if update {
				// One real-cost comparison covers authentication; fixtures cover the rest.
				if _, _, err := s.login(u.Username, input.Password); err != nil {
					t.Fatal("updated password cannot authenticate:", err)
				}
				if _, err := s.authenticate(oldToken); err == nil {
					t.Fatal("password update did not revoke the old login")
				}
			}
		})
	}
}
