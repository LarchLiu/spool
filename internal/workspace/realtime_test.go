package workspace

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type realtimeMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func realtimeFixture(t *testing.T, configure ...func(*App)) (*App, *httptest.Server, User, Project, *http.Cookie, string) {
	t.Helper()
	s := testStore(t)
	alice := testUser(t, s, "alice")
	p := testProject(t, s, alice)
	testAgent(t, s)
	a := NewApp(s, &fakeRunner{}, "", "")
	for _, config := range configure {
		config(a)
	}
	server := httptest.NewServer(a)
	t.Cleanup(func() { a.Close(); server.Close() })
	cookie, csrf := loginRequest(t, a, alice.Username)
	return a, server, alice, p, cookie, csrf
}

func realtimeConnect(t *testing.T, server *httptest.Server, cookie *http.Cookie, csrf string, visible bool) *websocket.Conn {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1)+"/api/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	conn.SetReadLimit(4 << 20)
	realtimeWrite(t, conn, realtimeInput{Type: "hello", CSRF: csrf, Visible: visible})
	return conn
}

func realtimeWrite(t *testing.T, conn *websocket.Conn, value realtimeInput) {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	data, _ := json.Marshal(value)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func realtimeUntil(t *testing.T, conn *websocket.Conn, match func(realtimeMessage) bool) realtimeMessage {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message realtimeMessage
		if err = json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "ping" {
			realtimeWrite(t, conn, realtimeInput{Type: "pong", Visible: true})
		}
		if match(message) {
			return message
		}
	}
}

func realtimeReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "ready" })
}

func TestRealtimePublishesAuthorizedNavigationAndActors(t *testing.T) {
	a, server, alice, project, cookie, csrf := realtimeFixture(t)
	bob := testUser(t, a.store, "bob")
	project.Members = append(project.Members, bob.ID)
	project.SharedChats = true
	if _, err := a.store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	conn := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, conn)
	chat, err := a.store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.beginTurnBy(chat.ID, "hello", chatmodel.BackendCodex, "", nil, bob.ID); err != nil {
		t.Fatal(err)
	}
	realtimeUntil(t, conn, func(m realtimeMessage) bool {
		if m.Type != "navigation" {
			return false
		}
		if strings.Contains(string(m.Data), `"avatar"`) || strings.Contains(string(m.Data), `"nickname"`) {
			t.Fatal("navigation contains mutable profile data", string(m.Data))
		}
		var nav Navigation
		if err := json.Unmarshal(m.Data, &nav); err != nil {
			t.Fatal(err)
		}
		return len(nav.Chats) == 1 && nav.Chats[0].ID == chat.ID && len(nav.Chats[0].Participants) == 1 && nav.Chats[0].Participants[0].ID == bob.ID
	})
	before, _ := a.store.Get(chat.ID)
	if err = a.store.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "bob-new"}); err != nil {
		t.Fatal(err)
	}
	realtimeUntil(t, conn, func(m realtimeMessage) bool {
		return m.Type == "profiles" && strings.Contains(string(m.Data), "bob-new")
	})
	after, _ := a.store.Get(chat.ID)
	if before.UpdatedAt != after.UpdatedAt {
		t.Fatal("profile update changed chat revision")
	}
	// A global invalidation must not expose other users' private project data.
	private := testProject(t, a.store, bob)
	if _, err = a.store.CreateChat(bob.ID, private.ID, chatmodel.BackendCodex); err != nil {
		t.Fatal(err)
	}
	project.Members = []string{bob.ID}
	if _, err = a.store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	message := realtimeUntil(t, conn, func(m realtimeMessage) bool {
		return m.Type == "navigation" && strings.Contains(string(m.Data), `"projects":[]`)
	})
	if strings.Contains(string(message.Data), private.ID) || strings.Contains(string(message.Data), chat.ID) {
		t.Fatal("revoked/private navigation leaked", string(message.Data))
	}
}

func TestRealtimePresenceTracksConnectionsVisibilityAndClose(t *testing.T) {
	a, server, alice, _, cookie, csrf := realtimeFixture(t)
	first := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, first)
	second := realtimeConnect(t, server, cookie, csrf, false)
	realtimeReady(t, second)
	waitPresence := func(devices int, state string) {
		t.Helper()
		realtimeUntil(t, first, func(m realtimeMessage) bool {
			if m.Type != "presence" {
				return false
			}
			if strings.Contains(string(m.Data), `"avatar"`) || strings.Contains(string(m.Data), `"nickname"`) {
				t.Fatal("presence contains mutable profile data", string(m.Data))
			}
			var users []PresenceUser
			json.Unmarshal(m.Data, &users)
			return len(users) == 1 && users[0].ID == alice.ID && users[0].Devices == devices && users[0].State == state
		})
	}
	waitPresence(2, "online")
	realtimeWrite(t, first, realtimeInput{Type: "visibility", Visible: false})
	waitPresence(2, "away")
	second.CloseNow()
	waitPresence(1, "away")
	first.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.presence.List(a.store)) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("closed connection remained online")
}

func TestRealtimeRejectsUnauthenticatedCrossOriginAndInvalidCSRF(t *testing.T) {
	a, server, _, _, cookie, csrf := realtimeFixture(t)
	for _, tc := range []struct {
		cookie *http.Cookie
		origin string
		status int
	}{
		{nil, server.URL, http.StatusUnauthorized},
		{cookie, "https://evil.example", http.StatusForbidden},
		{cookie, "", http.StatusForbidden},
	} {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		headers := http.Header{"Origin": {tc.origin}}
		if tc.cookie != nil {
			headers.Set("Cookie", tc.cookie.String())
		}
		conn, response, err := websocket.Dial(ctx, server.URL+"/api/events", &websocket.DialOptions{HTTPHeader: headers})
		done()
		if conn != nil {
			conn.CloseNow()
		}
		if err == nil || response == nil || response.StatusCode != tc.status {
			t.Fatalf("origin=%q: response=%v err=%v", tc.origin, response, err)
		}
	}
	conn := realtimeConnect(t, server, cookie, csrf+"invalid", true)
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("invalid CSRF was accepted:", err)
	}
	if len(a.presence.List(a.store)) != 0 {
		t.Fatal("unauthorized connection became present")
	}
}

func TestRealtimeRevocationAndShutdownCloseConnections(t *testing.T) {
	for _, action := range []string{"logout", "disable", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			a, server, alice, _, cookie, csrf := realtimeFixture(t)
			conn := realtimeConnect(t, server, cookie, csrf, true)
			realtimeReady(t, conn)
			switch action {
			case "logout":
				if w := apiRequest(a, "POST", "/api/logout", `{}`, cookie, csrf); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "disable":
				if err := a.store.DeleteUser(alice.ID); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
			}
			ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("connection was not closed")
					}
					return
				}
			}
		})
	}
}

func TestRealtimeHeartbeatTimeoutAndReconnectSnapshot(t *testing.T) {
	a, server, alice, project, cookie, csrf := realtimeFixture(t, func(a *App) {
		a.realtimePing = 20 * time.Millisecond
		a.realtimeTimeout = 100 * time.Millisecond
	})
	conn := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, conn)
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	sawPing := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || !sawPing {
				t.Fatal("missing ping or timeout:", err)
			}
			break
		}
		var message realtimeMessage
		json.Unmarshal(data, &message)
		sawPing = sawPing || message.Type == "ping"
		// Do not answer pings: simulate a stalled/background-suspended page.
	}
	chat, err := a.store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	reconnected := realtimeConnect(t, server, cookie, csrf, true)
	realtimeUntil(t, reconnected, func(m realtimeMessage) bool {
		return m.Type == "navigation" && strings.Contains(string(m.Data), chat.ID)
	})
}

func TestChangeHubCoalescesAndNotifiesOnlyCommittedChanges(t *testing.T) {
	s := testStore(t)
	sub := s.changes.subscribe()
	defer sub.close()
	for i := 0; i < 100; i++ {
		s.changes.publish(changeNavigation)
		s.changes.publish(changePresence)
	}
	<-sub.wake
	if got := sub.take(); got != changeNavigation|changePresence {
		t.Fatalf("lost changes: %v", got)
	}
	s.notify(errors.New("rollback"), changeAgents)
	select {
	case <-sub.wake:
		t.Fatal("failed mutation emitted an event")
	default:
	}
}

func TestRealtimeHeartbeatPongsKeepPresenceAndExpiredSessionsClose(t *testing.T) {
	a, server, _, _, cookie, csrf := realtimeFixture(t, func(a *App) {
		a.realtimePing = 25 * time.Millisecond
		a.realtimeTimeout = 200 * time.Millisecond
	})
	conn := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, conn)
	until := time.Now().Add(450 * time.Millisecond)
	for time.Now().Before(until) {
		realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "ping" })
	}
	if len(a.presence.List(a.store)) != 1 {
		t.Fatal("responsive client expired")
	}
	if _, err := a.store.db.Exec(`UPDATE auth_sessions SET expires_at=0 WHERE token_hash=?`, tokenHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatal("expired session did not receive policy close:", err)
			}
			break
		}
	}
}

func TestRealtimeAllowsExactPublicProxyOriginAndCSP(t *testing.T) {
	// Simulate an HTTPS reverse proxy rewriting the upstream Host.
	a, server, _, _, cookie, csrf := realtimeFixture(t, func(a *App) {
		a.settings.PublicURL = "https://chat.example.test"
	})
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	conn, _, err := websocket.Dial(ctx, server.URL+"/api/events", &websocket.DialOptions{HTTPHeader: http.Header{
		"Cookie": {cookie.String()}, "Origin": {a.settings.PublicURL},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	realtimeWrite(t, conn, realtimeInput{Type: "hello", CSRF: csrf, Visible: true})
	realtimeReady(t, conn)
	for _, tc := range []struct {
		host string
		tls  bool
		want string
	}{
		{"127.0.0.1:8787", false, "'self' ws://127.0.0.1:8787 wss://chat.example.test"},
		{"chat.example.test", true, "'self' wss://chat.example.test wss://chat.example.test"},
		{"bad;script-src", false, "'self' wss://chat.example.test"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = tc.host
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := a.realtimeConnectSources(r); got != tc.want {
			t.Errorf("host=%s: CSP sources=%q want=%q", tc.host, got, tc.want)
		}
	}
}

func TestRealtimeFinishingAndApprovalPublishNavigation(t *testing.T) {
	a, server, alice, project, cookie, csrf := realtimeFixture(t)
	chat, _ := a.store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	turn, err := a.store.beginTurnBy(chat.ID, "hello", chatmodel.BackendCodex, "", nil, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	conn := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, conn)
	if _, err = a.store.recordTurnAction(chat.ID, turn.ID, alice.ID, "approve", "approval-1"); err != nil {
		t.Fatal(err)
	}
	realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "navigation" })
	if err = a.store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	realtimeUntil(t, conn, func(m realtimeMessage) bool {
		return m.Type == "navigation" && strings.Contains(string(m.Data), `"status":"completed"`)
	})
}
