package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

func (p *panel) desktopServerSession(server desktopServer) *mygo.Session {
	// Keep the local account and its notifications in the existing store. Before
	// macOS 14 named stores do not persist, so retain the gateway isolation there.
	if server.ID == localDesktopServer || !persistentDesktopSessions() {
		return mygo.DefaultSession()
	}
	// Scope native data to the profile directory too: smoke runs and alternate
	// --data-dir instances must never open a developer's session store.
	key := sha256.Sum256([]byte(p.serversPath + "\x00" + desktopServerMount(server)))
	name := fmt.Sprintf("spool-server-%x", key)
	if p.serverSessions == nil {
		p.serverSessions = make(map[string]*mygo.Session)
	}
	if session := p.serverSessions[name]; session != nil {
		return session
	}
	var session *mygo.Session
	if p.sessionsEphemeral {
		session = mygo.NewEphemeralSession()
	} else {
		session = mygo.NewSession(name)
	}
	p.serverSessions[name] = session
	return session
}

type desktopCookieStore interface {
	List(mygo.CookieFilter) ([]mygo.Cookie, error)
	Set(mygo.Cookie) error
	Delete(mygo.Cookie) error
}

const desktopSessionMigrationCookie = "spool_native_session_v1"

// Copy only this mount's cookies, never those of the local account or another
// server. A durable marker prevents a later logout from restoring an old login.
// Remove originals only after every copy and the marker have been written.
func migrateDesktopSession(ctx context.Context, legacy, destination desktopCookieStore, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || !strings.HasPrefix(u.Path, "/web/") {
		return fmt.Errorf("invalid desktop session address")
	}
	filter := mygo.CookieFilter{URL: address, Path: u.Path}
	current, err := destination.List(filter)
	if err != nil {
		return err
	}
	migrated := false
	for _, cookie := range current {
		if cookie.Name == desktopSessionMigrationCookie {
			migrated = true
		}
	}
	old, err := legacy.List(filter)
	if err != nil {
		return err
	}
	prefix := "spool_remote_" + strings.TrimSuffix(strings.TrimPrefix(u.Path, "/web/"), "/") + "_"
	var owned []mygo.Cookie
	for _, cookie := range old {
		if cookie.Path == u.Path && strings.HasPrefix(cookie.Name, prefix) {
			owned = append(owned, cookie)
		}
	}
	// Existing cookies are authoritative. Finish a partially written migration
	// without replacing a newer token; a completed migration never imports again.
	if !migrated {
		for _, cookie := range owned {
			if err := ctx.Err(); err != nil {
				return err
			}
			exists := false
			for _, previous := range current {
				exists = exists || previous.Name == cookie.Name && previous.Path == cookie.Path
			}
			if exists {
				continue
			}
			if err := destination.Set(cookie); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := destination.Set(mygo.Cookie{Name: desktopSessionMigrationCookie, Value: "1", Domain: u.Hostname(), Path: u.Path, HTTPOnly: true, SameSite: mygo.CookieSameSiteStrict, Expires: time.Now().AddDate(10, 0, 0)}); err != nil {
			return err
		}
	}
	for _, cookie := range owned {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := legacy.Delete(cookie); err != nil {
			return err
		}
	}
	return nil
}

func (p *panel) prepareDesktopChatSession(id string, chat *desktopServerChat) {
	if chat.sessionPreparing {
		return
	}
	chat.sessionPreparing = true
	address, session := chat.address, chat.session
	p.serverNetworks.run(func(ctx context.Context) {
		err := migrateDesktopSession(ctx, mygo.Cookies, session.Cookies, address)
		p.window.Update(func() {
			if p.quitting || p.serverChats[id] != chat {
				return
			}
			chat.sessionPreparing = false
			if err == nil {
				chat.sessionReady = true
				err = chat.view.Page().LoadURL(chat.address)
			}
			if err != nil {
				log.Printf("event=desktop_session_prepare_failed server=%q", id)
				chat.failed = true
				p.recordDesktopServerState(id, "error")
				if p.chatView == chat.view {
					p.chatFailed = true
				}
			}
		})
	})
}
