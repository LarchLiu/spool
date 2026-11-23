package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/egoist/mygo"

	"spool"
)

// Exercise native windows with synthetic desktop pages, without screen capture,
// native input injection, provider accounts or a developer's service data.
func (p *panel) probeRemoteDesktopWindows(ctx context.Context) error {
	assets, err := fs.Sub(spool.Frontend(), "frontend")
	if err != nil {
		return err
	}
	files := http.FileServer(http.FS(assets))
	mount := "/web/" + strings.Repeat("a", 52) + "/"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, mount) {
			r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, mount)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"csrf": "synthetic-csrf"})
		case "/api/desktop":
			_, _ = w.Write([]byte(`{"available":true}`))
		case "/api/desktop/connect":
			_, _ = w.Write([]byte(`{"token":"synthetic-view"}`))
		case "/api/desktop/disconnect":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/desktop/view/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><h1>Synthetic desktop</h1><textarea aria-label="Remote input"></textarea>`))
		default:
			w.Header().Del("Content-Type")
			files.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	const firstID, secondID = "smoke-remote-first", "smoke-remote-second"
	type windows struct {
		first, second *mygo.Window
		primary       *mygo.WebView
	}
	created := make(chan windows, 1)
	p.window.Update(func() {
		first := &desktopServerChat{address: server.URL + "/", view: p.chatView}
		second := &desktopServerChat{address: server.URL + mount, view: p.chatView}
		p.servers.Entries = append(p.servers.Entries,
			desktopServer{ID: firstID, Name: "Remote Alpha"},
			desktopServer{ID: secondID, Name: "Remote Beta"},
		)
		p.serverChats[firstID], p.serverChats[secondID] = first, second
		p.openRemoteDesktop(firstID, first)
		p.openRemoteDesktop(secondID, second)
		created <- windows{first.remoteDesktop, second.remoteDesktop, p.chatView}
	})
	var opened windows
	select {
	case opened = <-created:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		done := make(chan struct{})
		p.window.Update(func() {
			opened.first.Destroy()
			opened.second.Destroy()
			delete(p.serverChats, firstID)
			delete(p.serverChats, secondID)
			p.servers.Entries = p.servers.Entries[:len(p.servers.Entries)-2]
			close(done)
		})
		<-done
	}()
	for _, window := range []*mygo.Window{opened.first, opened.second} {
		if err := waitChatScript(ctx, window.Page(), `return document.querySelector('iframe')?.contentDocument?.querySelector('textarea') !== null && !!document.querySelector('iframe')?.contentDocument?.querySelector('textarea');`); err != nil {
			return fmt.Errorf("native remote viewer did not load: %w", err)
		}
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return p.desktopWindowCount() == 3 && p.chatView == opened.primary &&
			strings.Contains(opened.first.Title(), "Remote Alpha") &&
			strings.Contains(opened.second.Title(), "Remote Beta")
	}); err != nil {
		return fmt.Errorf("native remote windows or server titles: %w", err)
	}
	p.window.Update(func() {
		p.openRemoteDesktop(firstID, p.serverChats[firstID])
		p.servers.Entries[len(p.servers.Entries)-2].Name = "Renamed Alpha"
		p.syncRemoteDesktopTitle(firstID, p.serverChats[firstID])
	})
	if err := waitChatScript(ctx, opened.first.Page(), `return document.title.includes('Renamed Alpha') && document.querySelector('h2')?.textContent.includes('Renamed Alpha');`); err != nil {
		return fmt.Errorf("native remote title did not follow server rename: %w", err)
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return p.desktopWindowCount() == 3 && p.serverChats[firstID].remoteDesktop == opened.first
	}); err != nil {
		return fmt.Errorf("reopening remote desktop did not reuse its window: %w", err)
	}
	// The native close button and the viewer's disconnect action must each close
	// their own window, including a viewer created by Go rather than window.open.
	if _, err := opened.first.Page().EvalContext(ctx, `document.querySelector('.dialog-heading button').click();`); err != nil && !opened.first.IsDestroyed() {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return opened.first.IsDestroyed() && !opened.second.IsDestroyed() &&
			p.serverChats[firstID].remoteDesktop == nil && p.desktopWindowCount() == 2
	}); err != nil {
		return fmt.Errorf("disconnect affected the wrong native remote window: %w", err)
	}
	opened.second.Close()
	return p.waitDesktopServerUI(ctx, func() bool {
		return p.desktopWindowCount() == 1 && p.chatView == opened.primary && p.serverChats[secondID].remoteDesktop == nil
	})
}
