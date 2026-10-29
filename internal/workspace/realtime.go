package workspace

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"spool/internal/i18n"
	chatmodel "spool/internal/model"
)

type realtimeInput struct {
	Type    string `json:"type"`
	CSRF    string `json:"csrf,omitempty"`
	Visible bool   `json:"visible"`
}

func (a *App) realtimeConnectSources(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	sources := "'self'"
	for _, origin := range []string{scheme + "://" + r.Host, a.settings.PublicURL} {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || strings.IndexFunc(u.Host, func(c rune) bool {
			return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-:[]", c))
		}) >= 0 {
			continue
		}
		if u.Scheme == "http" {
			u.Scheme = "ws"
		} else if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			continue
		}
		// WebKit does not consistently include ws(s) in CSP 'self'. Explicitly
		// allow only this page's host and the configured public proxy origin.
		sources += " " + u.Scheme + "://" + u.Host
	}
	return sources
}

func (a *App) realtimeStream(w http.ResponseWriter, r *http.Request, identity authIdentity) {
	if r.Header.Get("Origin") == "" || !a.sameOrigin(r) {
		apiError(w, http.StatusForbidden, "请求来源或 CSRF 校验失败")
		return
	}
	a.turnMu.Lock()
	if a.closing {
		a.turnMu.Unlock()
		apiError(w, http.StatusServiceUnavailable, "agent service is stopping")
		return
	}
	a.streams.Add(1)
	a.turnMu.Unlock()
	defer a.streams.Done()
	// The app's exact-origin check also supports PublicURL behind a proxy that
	// rewrites Host. Never replace it with permissive origin host patterns.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4096)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Hijacked connections are not closed by http.Server.Close.
	go func() {
		select {
		case <-a.shutdown:
			cancel()
			conn.CloseNow()
		case <-ctx.Done():
		}
	}()
	helloCtx, helloCancel := context.WithTimeout(ctx, 5*time.Second)
	var hello realtimeInput
	err = readRealtime(helloCtx, conn, &hello)
	helloCancel()
	if err != nil {
		return
	}
	if hello.Type != "hello" || len(hello.CSRF) != len(identity.CSRF) || subtle.ConstantTimeCompare([]byte(hello.CSRF), []byte(identity.CSRF)) != 1 {
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid credentials")
		return
	}
	sub := a.store.changes.subscribe()
	defer sub.close()
	client := chatmodel.NewID()
	defer a.presence.Disconnect(identity.User.ID, client)
	a.presence.Touch(identity, client, hello.Visible)
	last := make(map[string][]byte)
	send := func(kind string, value any) error {
		data, err := json.Marshal(value)
		if err != nil || bytes.Equal(last[kind], data) {
			return err
		}
		payload, err := json.Marshal(struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}{kind, data})
		if err != nil {
			return err
		}
		writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		if err = conn.Write(writeCtx, websocket.MessageText, payload); err == nil {
			last[kind] = data
		}
		return err
	}
	syncState := func(changes changeKind) bool {
		// Revalidate before every snapshot, not just at connection time. This
		// prevents revoked sessions from receiving subsequent change events.
		if _, err := a.identity(r); err != nil {
			a.presence.Remove(identity.TokenHash)
			_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
			return false
		}
		if changes&changeNavigation != 0 {
			navigation, err := a.store.UserNavigation(identity.User.ID)
			if err != nil || send("navigation", navigation) != nil {
				return false
			}
		}
		if changes&changeAgents != 0 {
			agents, err := a.store.Agents()
			if err != nil {
				return false
			}
			for i := range agents {
				if agents[i].Error != "" {
					agents[i].Error = i18n.Translate(w.Header().Get("Content-Language"), "Model discovery failed. The last successful list is retained. Contact your administrator.")
				}
			}
			if send("agents", agents) != nil {
				return false
			}
		}
		return changes&changePresence == 0 || send("presence", a.presence.List(a.store)) == nil
	}
	if !syncState(changeNavigation|changeAgents|changePresence) || send("ready", true) != nil {
		return
	}
	incoming := make(chan realtimeInput)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			readCtx, done := context.WithTimeout(ctx, a.realtimeTimeout)
			var input realtimeInput
			err := readRealtime(readCtx, conn, &input)
			done()
			if err != nil {
				return
			}
			select {
			case incoming <- input:
			case <-ctx.Done():
				return
			}
		}
	}()
	// Wait for the reader before returning so shutdown never leaves a goroutine
	// accessing app state after the owning service closes its store.
	defer func() { cancel(); conn.CloseNow(); <-readDone }()
	ticker := time.NewTicker(a.realtimePing)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case <-sub.wake:
			if !syncState(sub.take()) {
				return
			}
		case input := <-incoming:
			if input.Type != "pong" && input.Type != "visibility" && input.Type != "refresh" {
				_ = conn.Close(websocket.StatusPolicyViolation, "invalid message")
				return
			}
			current, err := a.identity(r)
			if err != nil {
				a.presence.Remove(identity.TokenHash)
				_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			a.presence.Touch(current, client, input.Visible)
			if input.Type == "refresh" && !syncState(changeNavigation|changeAgents|changePresence) {
				return
			}
		case <-ticker.C:
			if !syncState(changeAuth | changePresence) {
				return
			}
			// Application-level ping/pong also lets browsers detect a stalled
			// connection; the browser API hides protocol-level control frames.
			delete(last, "ping")
			if send("ping", true) != nil {
				return
			}
		}
	}
}

func readRealtime(ctx context.Context, conn *websocket.Conn, input *realtimeInput) error {
	kind, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if kind != websocket.MessageText {
		return websocket.CloseError{Code: websocket.StatusUnsupportedData, Reason: "text messages required"}
	}
	return json.Unmarshal(data, input)
}
