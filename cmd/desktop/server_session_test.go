package main

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo"
)

type sessionCookieFixture struct {
	cookies []mygo.Cookie
	failSet bool
}

func (s *sessionCookieFixture) List(mygo.CookieFilter) ([]mygo.Cookie, error) {
	return append([]mygo.Cookie(nil), s.cookies...), nil
}
func (s *sessionCookieFixture) Set(cookie mygo.Cookie) error {
	if s.failSet {
		return errors.New("store unavailable")
	}
	for i, previous := range s.cookies {
		if previous.Name == cookie.Name && previous.Path == cookie.Path {
			s.cookies[i] = cookie
			return nil
		}
	}
	s.cookies = append(s.cookies, cookie)
	return nil
}
func (s *sessionCookieFixture) Delete(cookie mygo.Cookie) error {
	for i, previous := range s.cookies {
		if previous.Name == cookie.Name && previous.Path == cookie.Path {
			s.cookies = append(s.cookies[:i], s.cookies[i+1:]...)
			return nil
		}
	}
	return nil
}

func TestDesktopSessionMigrationKeepsOtherAccountsAndDoesNotRestoreLogout(t *testing.T) {
	local := mygo.Cookie{Name: "spool_session", Path: "/", Value: "local", HTTPOnly: true}
	remote := mygo.Cookie{Name: "spool_remote_first_spool_session", Path: "/web/first/", Value: "remote", HTTPOnly: true}
	other := mygo.Cookie{Name: "spool_remote_second_spool_session", Path: "/web/second/", Value: "other", HTTPOnly: true}
	legacy := &sessionCookieFixture{cookies: []mygo.Cookie{local, remote, other}}
	destination := &sessionCookieFixture{}
	const address = "http://127.0.0.1:1234/web/first/"
	if err := migrateDesktopSession(t.Context(), legacy, destination, address); err != nil {
		t.Fatal(err)
	}
	if len(legacy.cookies) != 2 || legacy.cookies[0] != local || legacy.cookies[1] != other {
		t.Fatal("migration changed another account")
	}
	if len(destination.cookies) != 2 || destination.cookies[0] != remote || destination.cookies[1].Name != desktopSessionMigrationCookie {
		t.Fatalf("session was not migrated: %+v", destination.cookies)
	}
	// Simulate logout and an old cookie that survived a previous interrupted cleanup.
	_ = destination.Delete(remote)
	_ = legacy.Set(remote)
	if err := migrateDesktopSession(t.Context(), legacy, destination, address); err != nil {
		t.Fatal(err)
	}
	if len(destination.cookies) != 1 || destination.cookies[0].Name != desktopSessionMigrationCookie {
		t.Fatal("migration restored a logged-out account")
	}
	if len(legacy.cookies) != 2 {
		t.Fatal("migration did not retry cleaning up the old login")
	}
}

func TestDesktopSessionMigrationCompletesPartialCopyWithoutReplacingNewerCookies(t *testing.T) {
	old := mygo.Cookie{Name: "spool_remote_first_spool_session", Path: "/web/first/", Value: "old", HTTPOnly: true}
	additional := mygo.Cookie{Name: "spool_remote_first_preference", Path: old.Path, Value: "preference"}
	newer := old
	newer.Value = "newer"
	legacy := &sessionCookieFixture{cookies: []mygo.Cookie{old, additional}}
	destination := &sessionCookieFixture{cookies: []mygo.Cookie{newer}}
	if err := migrateDesktopSession(t.Context(), legacy, destination, "http://127.0.0.1:1234/web/first/"); err != nil {
		t.Fatal(err)
	}
	if len(legacy.cookies) != 0 || len(destination.cookies) != 3 || destination.cookies[0] != newer || destination.cookies[1] != additional {
		t.Fatalf("partial copy did not preserve newer data and finish migration: %+v", destination.cookies)
	}
}

func TestDesktopSessionMigrationPreservesNewerDestinationAndRetriesFailure(t *testing.T) {
	old := mygo.Cookie{Name: "spool_remote_first_spool_session", Path: "/web/first/", Value: "old", HTTPOnly: true}
	legacy := &sessionCookieFixture{cookies: []mygo.Cookie{old}}
	destination := &sessionCookieFixture{failSet: true}
	const address = "http://127.0.0.1:1234/web/first/"
	if err := migrateDesktopSession(t.Context(), legacy, destination, address); err == nil || len(legacy.cookies) != 1 {
		t.Fatal("failed copy removed the original login")
	}
	newer := old
	newer.Value = "newer"
	destination.failSet = false
	destination.cookies = []mygo.Cookie{newer}
	if err := migrateDesktopSession(t.Context(), legacy, destination, address); err != nil {
		t.Fatal(err)
	}
	if destination.cookies[0] != newer {
		t.Fatal("migration replaced a newer account")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	destination.cookies = nil
	if err := migrateDesktopSession(ctx, legacy, destination, address); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration ignored shutdown: %v", err)
	}
}

func TestDesktopSessionsAreStableAndScopedToProfileAndRoute(t *testing.T) {
	if !persistentDesktopSessions() {
		t.Skip("named stores do not persist on this OS")
	}
	server := desktopServer{ID: "remote", Address: "https://one.example", Mode: "direct"}
	p := &panel{serversPath: t.TempDir() + "/desktop-servers.json"}
	first := p.desktopServerSession(server)
	if first != p.desktopServerSession(server) || first == mygo.DefaultSession() {
		t.Fatal("remote session is not stable and independent")
	}
	server.Address = "https://two.example"
	if first == p.desktopServerSession(server) {
		t.Fatal("changing destination reused another server's data")
	}
	other := &panel{serversPath: t.TempDir() + "/desktop-servers.json"}
	server.Address = "https://one.example"
	if first == other.desktopServerSession(server) {
		t.Fatal("alternate data directories shared a login")
	}
	if p.desktopServerSession(desktopServer{ID: localDesktopServer}) != mygo.DefaultSession() {
		t.Fatal("local account was moved")
	}
}
