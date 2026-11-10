package workspace

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/testutil/voxlfixture"
	"spool/internal/voxl"
)

func remoteDesktopFixture(t *testing.T) (*Store, *App, *voxl.Manager) {
	t.Helper()
	store := testStore(t)
	manager := voxl.New(filepath.Join(t.TempDir(), "voxl"), func() { store.changes.publish(changeRemoteDesktop) })
	voxlfixture.Install(t, manager.Status().Directory)
	t.Cleanup(manager.Stop)
	app := NewApp(store, &fakeRunner{}, "", "")
	app.desktop = manager
	t.Cleanup(func() { _ = app.Close() })
	return store, app, manager
}

func remoteDesktopToken(t *testing.T, app *App, cookie *http.Cookie, csrf string) string {
	t.Helper()
	w := apiRequest(app, "POST", "/api/desktop/connect", `{}`, cookie, csrf)
	var result struct{ Token string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Token == "" {
		t.Fatal("could not create desktop view", w.Code, w.Body)
	}
	return result.Token
}

func TestRealtimeRemoteDesktopAvailabilityIsAuthorizedAndTracksServiceChanges(t *testing.T) {
	store, app, manager := remoteDesktopFixture(t)
	alice := testUser(t, store, "alice")
	if _, err := store.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: alice.Nickname, RemoteDesktop: true}); err != nil {
		t.Fatal(err)
	}
	testUser(t, store, "bob")
	aliceCookie, aliceCSRF := loginRequest(t, app, "alice")
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	available := func(conn *websocket.Conn, want bool) {
		t.Helper()
		message := realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "desktop" })
		var value map[string]bool
		if err := json.Unmarshal(message.Data, &value); err != nil || len(value) != 1 || value["available"] != want {
			t.Fatalf("desktop snapshot=%s want available=%v: %v", message.Data, want, err)
		}
	}
	conn := realtimeConnect(t, server, aliceCookie, aliceCSRF, true)
	available(conn, false)
	realtimeReady(t, conn)
	manager.SetEnabled(t.Context(), true)
	available(conn, true)
	if manager.Status().Running {
		t.Fatal("pushing availability must not start desktop capture")
	}
	denied := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	available(denied, false)
	realtimeReady(t, denied)
	manager.SetEnabled(t.Context(), false)
	available(conn, false)
	manager.SetEnabled(t.Context(), true)
	available(conn, true)
	// A fresh identity must be used even when a grant changes without replacing the session.
	if _, err := store.db.Exec(`UPDATE user_remote_desktop SET enabled=0 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	store.changes.publish(changeRemoteDesktop)
	available(conn, false)
	if manager.Status().Running {
		t.Fatal("authorization snapshots started desktop capture")
	}
}

func TestRemoteDesktopRequiresLocalEnablementAndSeparateUserGrant(t *testing.T) {
	store, app, manager := remoteDesktopFixture(t)
	alice := testUser(t, store, "alice")
	bob := testUser(t, store, "bob")
	project := testProject(t, store, alice, bob)
	_ = project // Project access must not imply host desktop access.
	cookie, csrf := loginRequest(t, app, "alice")
	available := func(want bool) {
		t.Helper()
		w := apiRequest(app, "GET", "/api/desktop", "", cookie, csrf)
		var status struct {
			Available bool `json:"available"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Available != want {
			t.Fatalf("availability=%s status=%d want=%v", w.Body, w.Code, want)
		}
	}
	available(false)
	manager.SetEnabled(t.Context(), true)
	available(false)
	if w := apiRequest(app, "GET", remoteDesktopPrefix+"/", "", cookie, csrf); w.Code != 403 {
		t.Fatal("project member without remote grant accessed desktop", w.Code)
	}
	_, err := store.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: alice.Nickname, RemoteDesktop: true})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf = loginRequest(t, app, "alice")
	available(true)
	token := remoteDesktopToken(t, app, cookie, csrf)
	if manager.Status().Running {
		t.Fatal("availability check started capture")
	}
	w := apiRequest(app, "GET", remoteDesktopPrefix+"/?token="+token, "", cookie, csrf)
	if w.Code != 200 || w.Body.String() != "fixture:/" || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated viewer did not proxy correctly", w.Code, w.Header(), w.Body)
	}
	_, _, _, release, err := manager.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, path := range []string{"/api/desktop/install", "/api/desktop/enable", remoteDesktopPrefix + "/files/upload", remoteDesktopPrefix + "/record/start"} {
		if w := apiRequest(app, "POST", path, `{}`, cookie, csrf); w.Code != 404 {
			t.Fatal("remote mutation endpoint exposed", path, w.Code)
		}
	}
	for _, path := range []string{remoteDesktopPrefix + "/screenshot", remoteDesktopPrefix + "/../settings"} {
		if w := apiRequest(app, "GET", path, "", cookie, csrf); w.Code != 404 {
			t.Fatal("unapproved helper API exposed", path, w.Code)
		}
	}
	for _, path := range []string{"/files/list", "/files/download"} {
		if w := apiRequest(app, "GET", remoteDesktopPrefix+path+"?path=hello.txt&token="+token, "", cookie, csrf); w.Code != 200 || w.Body.String() != "fixture:"+path {
			t.Fatal("authorized file resource did not proxy", path, w.Code, w.Body)
		}
	}
	upload := func(csrfToken, origin string, grantHeader bool, want int) {
		t.Helper()
		r := localRequest("PUT", remoteDesktopPrefix+"/files/upload?name=hello.txt&token="+token, strings.NewReader("fixture upload"))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrfToken)
		r.Header.Set("Origin", origin)
		if grantHeader {
			r.Header.Set("X-Voxl-Upload", "1")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want || want == 200 && w.Body.String() != "fixture upload" {
			t.Fatal("upload authorization/proxy failed", w.Code, w.Body)
		}
	}
	upload("", "http://127.0.0.1:8787", true, 403)
	upload(csrf, "https://evil.example", true, 403)
	upload(csrf, "http://127.0.0.1:8787", false, 404)
	upload(csrf, "http://127.0.0.1:8787", true, 200)
	if w := apiRequest(app, "GET", remoteDesktopPrefix+"/", "", nil, ""); w.Code != 401 {
		t.Fatal("anonymous viewer allowed", w.Code)
	}
	request := httptest.NewRequest("GET", remoteDesktopPrefix+"/", nil)
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("cross-origin viewer allowed", w.Code)
	}
	manager.SetEnabled(t.Context(), false)
	available(false)
}

func TestRemoteDesktopLocalDisconnectTracksUsersAndBlocksAutomaticReconnect(t *testing.T) {
	store, app, manager := remoteDesktopFixture(t)
	for _, name := range []string{"alice", "bob"} {
		user := testUser(t, store, name)
		if _, err := store.SaveUser(UserInput{ID: user.ID, Username: name, Nickname: name, RemoteDesktop: true}); err != nil {
			t.Fatal(err)
		}
	}
	aliceCookie, aliceCSRF := loginRequest(t, app, "alice")
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	alice, _ := store.authenticate(aliceCookie.Value)
	bob, _ := store.authenticate(bobCookie.Value)
	manager.SetEnabled(t.Context(), true)
	service := &Service{Store: store, app: app, desktop: manager}
	server := httptest.NewServer(app)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	aliceToken := remoteDesktopToken(t, app, aliceCookie, aliceCSRF)
	aliceToken2 := remoteDesktopToken(t, app, aliceCookie, aliceCSRF)
	bobToken := remoteDesktopToken(t, app, bobCookie, bobCSRF)
	if manager.Status().Running {
		t.Fatal("creating view credentials started capture")
	}
	for _, path := range []string{"/", "/ws", "/files/list", "/files/download"} {
		for _, token := range []string{"", "unknown-view"} {
			if w := apiRequest(app, "GET", remoteDesktopPrefix+path+"?token="+token, "", aliceCookie, aliceCSRF); w.Code != 403 {
				t.Fatal("resource accepted an unregistered view", path, w.Code)
			}
		}
	}
	otherSession, otherCSRF := loginRequest(t, app, "alice")
	for _, cookie := range []*http.Cookie{bobCookie, otherSession} {
		if w := apiRequest(app, "GET", remoteDesktopPrefix+"/?token="+aliceToken, "", cookie, ""); w.Code != 403 {
			t.Fatal("view credentials shared across users/sessions", w.Code)
		}
	}
	if w := apiRequest(app, "POST", "/api/desktop/connect", `{}`, aliceCookie, ""); w.Code != 403 {
		t.Fatal("view creation bypassed CSRF", w.Code)
	}
	dial := func(cookie *http.Cookie, token string) *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + remoteDesktopPrefix + "/ws?token=" + token
		conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {server.URL}}})
		if err != nil {
			t.Fatal("connect viewer", response, err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn
	}
	waitCounts := func(aliceCount, bobCount int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			counts := service.Status().RemoteDesktopConnections
			if counts[alice.User.ID] == aliceCount && counts[bob.User.ID] == bobCount {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("unexpected live connection counts", counts, aliceCount, bobCount)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	aliceConn := dial(aliceCookie, aliceToken)
	aliceConn2 := dial(aliceCookie, aliceToken2)
	bobConn := dial(bobCookie, bobToken)
	waitCounts(2, 1)
	for _, session := range []struct {
		cookie *http.Cookie
		csrf   string
	}{{bobCookie, bobCSRF}, {otherSession, otherCSRF}} {
		if w := apiRequest(app, "POST", "/api/desktop/disconnect?token="+aliceToken, `{}`, session.cookie, session.csrf); w.Code != 200 {
			t.Fatal("close foreign view", w.Code)
		}
	}
	waitCounts(2, 1)
	if err := service.DisconnectRemoteDesktopUser(alice.User.ID); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*websocket.Conn{aliceConn, aliceConn2} {
		readCtx, done := context.WithTimeout(ctx, 2*time.Second)
		_, _, err := conn.Read(readCtx)
		done()
		if err == nil || readCtx.Err() == context.DeadlineExceeded {
			t.Fatal("local disconnect did not close viewer promptly", err)
		}
	}
	waitCounts(0, 1)
	if !manager.Status().Running {
		t.Fatal("disconnecting Alice interrupted Bob's helper")
	}
	if err := bobConn.Write(ctx, websocket.MessageText, []byte("Bob stays connected")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := bobConn.Read(ctx); err != nil || string(data) != "Bob stays connected" {
		t.Fatal("other user's connection interrupted", string(data), err)
	}
	for _, path := range []string{remoteDesktopPrefix + "/?token=" + aliceToken, remoteDesktopPrefix + "/files/list?token=" + aliceToken} {
		if w := apiRequest(app, "GET", path, "", aliceCookie, aliceCSRF); w.Code != 403 {
			t.Fatal("revoked view reloaded", path, w.Code)
		}
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + remoteDesktopPrefix + "/ws?token=" + aliceToken
	retry, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {aliceCookie.String()}, "Origin": {server.URL}}})
	if err == nil {
		retry.CloseNow()
		t.Fatal("automatic reconnect accepted a revoked view")
	}
	if response == nil || response.StatusCode != 403 {
		t.Fatal("revoked reconnect not denied", response, err)
	}
	if w := apiRequest(app, "GET", "/api/desktop?token="+aliceToken, "", aliceCookie, aliceCSRF); w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatal("kicked dialog was not told to close", w.Code, w.Body)
	}
	current, err := store.authenticate(aliceCookie.Value)
	if err != nil || !current.User.RemoteDesktop || !app.remoteDesktopAvailable(current) {
		t.Fatal("manual disconnect changed the user's login or permission", current, err)
	}
	freshToken := remoteDesktopToken(t, app, aliceCookie, aliceCSRF)
	freshConn := dial(aliceCookie, freshToken)
	waitCounts(1, 1)
	if w := apiRequest(app, "POST", "/api/desktop/disconnect?token="+freshToken, `{}`, aliceCookie, aliceCSRF); w.Code != 200 {
		t.Fatal("explicit close failed", w.Code)
	}
	freshConn.CloseNow()
	waitCounts(0, 1)
	_, _, helperDone := manager.Endpoint()
	if err := service.DisconnectRemoteDesktopUser(bob.User.ID); err != nil {
		t.Fatal(err)
	}
	bobConn.CloseNow()
	waitCounts(0, 0)
	select {
	case <-helperDone:
	case <-time.After(3 * time.Second):
		t.Fatal("last administrator disconnect left capture running")
	}
}

func TestRemoteDesktopWebSocketStopsOnRevocationLogoutAndHelperStop(t *testing.T) {
	for _, scenario := range []string{"revoke", "logout", "stop", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			store, app, manager := remoteDesktopFixture(t)
			user := testUser(t, store, "alice")
			if _, err := store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname, RemoteDesktop: true}); err != nil {
				t.Fatal(err)
			}
			cookie, csrf := loginRequest(t, app, "alice")
			manager.SetEnabled(t.Context(), true)
			token := remoteDesktopToken(t, app, cookie, csrf)
			server := httptest.NewServer(app)
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			url := "ws" + strings.TrimPrefix(server.URL, "http") + remoteDesktopPrefix + "/ws?token=" + token
			for _, origin := range []string{"", "https://evil.example"} {
				bad, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {origin}}})
				if err == nil {
					bad.CloseNow()
					t.Fatal("WebSocket accepted missing/foreign origin", origin)
				}
			}
			if manager.Status().Running {
				t.Fatal("rejected origin started capture")
			}
			conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {server.URL}}})
			if err != nil {
				t.Fatal(err, response)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageBinary, []byte("desktop frame")); err != nil {
				t.Fatal(err)
			}
			kind, frame, err := conn.Read(ctx)
			if err != nil || kind != websocket.MessageBinary || string(frame) != "desktop frame" {
				t.Fatal("proxy changed WebSocket media", kind, string(frame), err)
			}
			_, _, helperDone := manager.Endpoint()
			switch scenario {
			case "revoke":
				_, err = store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname})
			case "logout":
				if w := apiRequest(app, "POST", "/api/logout", `{}`, cookie, csrf); w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			case "stop":
				manager.SetEnabled(t.Context(), false)
			case "disconnect":
				conn.CloseNow()
			}
			if err != nil {
				t.Fatal(err)
			}
			closedCtx, done := context.WithTimeout(t.Context(), 2*time.Second)
			defer done()
			if _, _, err := conn.Read(closedCtx); err == nil || closedCtx.Err() != nil {
				t.Fatal("existing desktop connection survived authorization/helper loss", err)
			}
			select {
			case <-helperDone:
			case <-time.After(2 * time.Second):
				t.Fatal("last disconnected viewer left capture running")
			}
			bad, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {"https://evil.example"}}})
			if err == nil {
				bad.CloseNow()
				t.Fatal("cross-origin WebSocket allowed")
			}
		})
	}
}

func TestRemoteDesktopGrantPersistsAndServiceOwnsHelperLifetime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPOOL_CODEX_BIN", filepath.Join(root, "no-codex"))
	t.Setenv("SPOOL_CLAUDE_BIN", filepath.Join(root, "no-claude"))
	service, err := OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := service.SetRemoteDesktopEnabled(true); err == nil {
		t.Fatal("missing helper enabled")
	}
	voxlfixture.Install(t, filepath.Join(root, "voxl"))
	settings, _ := service.Store.Settings()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	if err = service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = service.SetRemoteDesktopEnabled(true); err != nil {
		t.Fatal(err)
	}
	if service.VoxlStatus().Running {
		t.Fatal("helper started before Spool service")
	}
	if err = service.Start(); err != nil {
		t.Fatal(err)
	}
	if service.Status().Voxl.Running || !service.desktop.Available() {
		t.Fatal("enabled service must wait for a viewer before starting capture")
	}
	_, _, stopped, release, err := service.desktop.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = service.Stop(); err != nil {
		t.Fatal(err)
	}
	if service.VoxlStatus().Running {
		t.Fatal("helper survived service stop")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("service stop did not reap helper")
	}
	if err = service.Start(); err != nil {
		t.Fatal(err)
	}
	if service.VoxlStatus().Running {
		t.Fatal("service restart started capture without a connection")
	}
	_, _, stopped, releaseNext, err := service.desktop.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseNext()
	if err = service.SetRemoteDesktopEnabled(false); err != nil {
		t.Fatal(err)
	}
	if service.VoxlStatus().Running {
		t.Fatal("disabled helper remained running")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("admin switch did not reap helper")
	}
	address, _ := service.LocalWebURL()
	response, err := http.Get(address + "/healthz")
	if err != nil {
		t.Fatal("disabling desktop stopped Spool:", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	user := testUser(t, service.Store, "alice")
	if _, err = service.Store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname, RemoteDesktop: true}); err != nil {
		t.Fatal(err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	users, err := store.Users()
	if err != nil || len(users) != 1 || !users[0].RemoteDesktop {
		t.Fatal("grant not persisted", users, err)
	}
	if _, err = os.Stat(filepath.Join(root, "voxl", "bin")); err != nil {
		t.Fatal("installation removed on stop", err)
	}
}
