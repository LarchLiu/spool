package workspace

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/hkdf"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type pushTransport func(*http.Request) (*http.Response, error)

func (f pushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pushResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

func testPushSubscription(t *testing.T, suffix string) (webpush.Subscription, *ecdh.PrivateKey) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/" + suffix, Keys: webpush.Keys{P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))}}, key
}

func subscribePush(t *testing.T, a *App, cookie *http.Cookie, csrf string, sub webpush.Subscription, language string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"subscription": sub, "language": language})
	w := apiRequest(a, "POST", "/api/push/subscribe", string(body), cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
	}
}

func pushEndpointBody(sub webpush.Subscription) string {
	body, _ := json.Marshal(map[string]string{"endpoint": sub.Endpoint})
	return string(body)
}

func decryptPush(t *testing.T, request *http.Request, receiver *ecdh.PrivateKey, sub webpush.Subscription) map[string]string {
	t.Helper()
	data, err := io.ReadAll(request.Body)
	if err != nil || len(data) < 103 || data[20] != 65 {
		t.Fatalf("bad encrypted record: %d %v", len(data), err)
	}
	sender, err := ecdh.P256().NewPublicKey(data[21:86])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := receiver.ECDH(sender)
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	derive := func(secret, salt, info []byte, length int) []byte {
		out := make([]byte, length)
		if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	info := append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...)
	info = append(info, sender.Bytes()...)
	ikm := derive(shared, auth, info, 32)
	key := derive(ikm, data[:16], []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := derive(ikm, data[:16], []byte("Content-Encoding: nonce\x00"), 12)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, data[86:], nil)
	if err != nil {
		t.Fatal(err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatal("bad final record delimiter")
	}
	var payload map[string]string
	if err := json.Unmarshal(plain[:len(plain)-1], &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestPushEndpointAndKeyValidation(t *testing.T) {
	sub, _ := testPushSubscription(t, "valid")
	for _, endpoint := range []string{"https://fcm.googleapis.com/fcm/send/id", "https://updates.push.services.mozilla.com/wpush/v2/id", "https://web.push.apple.com/id", "https://wns2.notify.windows.com/w/?token=id"} {
		if !validPushEndpoint(endpoint) {
			t.Errorf("rejected provider: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"http://fcm.googleapis.com/id", "https://127.0.0.1/id", "https://[::1]/id", "https://192.168.0.1/id", "https://example.com/id", "https://fcm.googleapis.com.evil.test/id", "https://evilnotify.windows.com/id", "https://fcm.googleapis.com:8443/id", "https://user@fcm.googleapis.com/id", "https://fcm.googleapis.com/id#fragment", strings.Repeat("x", 5000)} {
		if validPushEndpoint(endpoint) {
			t.Errorf("accepted unsafe provider: %s", endpoint)
		}
	}
	if !validPushSubscription(sub) {
		t.Fatal("rejected real P-256 subscription")
	}
	for _, keys := range []webpush.Keys{{Auth: sub.Keys.Auth, P256dh: "invalid"}, {Auth: "AA", P256dh: sub.Keys.P256dh}, {Auth: sub.Keys.Auth, P256dh: base64.RawURLEncoding.EncodeToString(make([]byte, 65))}} {
		bad := sub
		bad.Keys = keys
		if validPushSubscription(bad) {
			t.Fatal("accepted invalid encryption keys")
		}
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1", "fd00::1", "::ffff:127.0.0.1", "fe80::1"} {
		if publicPushAddress(netip.MustParseAddr(value)) {
			t.Errorf("accepted non-public destination %s", value)
		}
	}
	if !publicPushAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("rejected public address")
	}
	client := newPushHTTPClient()
	defer client.CloseIdleConnections()
	if client.Transport.(*http.Transport).Proxy != nil || client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || client.Timeout == 0 {
		t.Fatal("push transport permits proxy/redirects or unbounded requests")
	}
}

func TestPushKeysPersistAndStayPrivate(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.webPushKeys()
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	next, err := s.webPushKeys()
	if err != nil || first != next || len(next.private) == 0 || len(next.public) == 0 {
		t.Fatalf("keys did not survive restart: %v", err)
	}
	u := testUser(t, s, "alice")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, _ := loginRequest(t, a, u.Username)
	w := apiRequest(a, "GET", "/api/push/config", "", cookie, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), first.private) || strings.Contains(w.Body.String(), "private") || !strings.Contains(w.Body.String(), first.public) {
		t.Fatalf("bad public configuration: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM settings WHERE value LIKE ?`, "%"+first.private+"%").Scan(&count); err != nil || count != 0 {
		t.Fatal("VAPID private key leaked into public settings")
	}
}

func TestPushSubscriptionAPIIsolationAndLogout(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, alice.Username)
	other, otherCSRF := loginRequest(t, a, bob.Username)
	sub, _ := testPushSubscription(t, "first")
	body, _ := json.Marshal(map[string]any{"subscription": sub, "language": "zh-CN"})
	for _, test := range []struct {
		cookie *http.Cookie
		csrf   string
		status int
	}{{nil, "", 401}, {cookie, "", 403}, {cookie, otherCSRF, 403}} {
		if w := apiRequest(a, "POST", "/api/push/subscribe", string(body), test.cookie, test.csrf); w.Code != test.status {
			t.Fatalf("mutation escaped auth/CSRF: %d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/push/subscribe", bytes.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin subscription accepted")
	}
	subscribePush(t, a, cookie, csrf, sub, "zh-CN")
	subscribePush(t, a, cookie, csrf, sub, "ja")
	var count int
	s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate device subscription")
	}
	for _, path := range []string{"status", "unsubscribe"} {
		w := apiRequest(a, "POST", "/api/push/"+path, pushEndpointBody(sub), other, otherCSRF)
		if w.Code != 200 || strings.Contains(w.Body.String(), "true") {
			t.Fatalf("other account can inspect/change subscription: %s", w.Body.String())
		}
	}
	if w := apiRequest(a, "POST", "/api/push/test", pushEndpointBody(sub), other, otherCSRF); w.Code != 404 {
		t.Fatal("other account can send test")
	}
	if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), cookie, csrf); !strings.Contains(w.Body.String(), "true") {
		t.Fatal("other account removed owner's subscription")
	}
	if w := apiRequest(a, "POST", "/api/push/unsubscribe", pushEndpointBody(sub), cookie, csrf); w.Code != 200 {
		t.Fatal("unsubscribe failed")
	}
	refresh, _ := json.Marshal(map[string]string{"endpoint": sub.Endpoint, "language": "en"})
	apiRequest(a, "POST", "/api/push/refresh", string(refresh), cookie, csrf)
	s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&count)
	if count != 0 {
		t.Fatal("page refresh revived disabled notifications")
	}
	subscribePush(t, a, cookie, csrf, sub, "en")
	next, nextCSRF := loginRequest(t, a, alice.Username)
	subscribePush(t, a, next, nextCSRF, sub, "en")
	apiRequest(a, "POST", "/api/logout", "{}", cookie, csrf)
	s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&count)
	if count != 1 {
		t.Fatal("old session logout removed device bound to newer login")
	}
	apiRequest(a, "POST", "/api/logout", "{}", next, nextCSRF)
	s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&count)
	if count != 0 {
		t.Fatal("logout did not remove push subscription")
	}
}

func TestPushTestEncryptsSignsAndPrunesExpiredSubscriptions(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	sub, private := testPushSubscription(t, "encrypted")
	subscribePush(t, a, cookie, csrf, sub, "zh-CN")
	keys, _ := s.webPushKeys()
	status := http.StatusCreated
	count := 0
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		count++
		if r.URL.String() != sub.Endpoint || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "300" {
			t.Fatal("missing encrypted Web Push headers")
		}
		payload := decryptPush(t, r, private, sub)
		if payload["body"] != "此设备的推送通知已正常工作。" || payload["url"] != "/" || payload["title"] != "Spool · 测试通知" {
			t.Fatalf("unexpected decrypted notification: %v", payload)
		}
		token := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "vapid t="), ", k=")[0]
		public, _ := base64.RawURLEncoding.DecodeString(keys.public)
		x, y := elliptic.Unmarshal(elliptic.P256(), public)
		parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil }, jwt.WithValidMethods([]string{"ES256"}))
		if err != nil || !parsed.Valid || parsed.Claims.(jwt.MapClaims)["aud"] != "https://fcm.googleapis.com" {
			t.Fatalf("bad VAPID signature: %v", err)
		}
		return pushResponse(status), nil
	})}
	if w := apiRequest(a, "POST", "/api/push/test", pushEndpointBody(sub), cookie, csrf); w.Code != 200 {
		t.Fatalf("test failed: %d %s", w.Code, w.Body.String())
	}
	if w := apiRequest(a, "POST", "/api/push/test", pushEndpointBody(sub), cookie, csrf); w.Code != 429 || count != 1 {
		t.Fatal("test notifications are not rate limited")
	}
	for _, code := range []int{http.StatusNotFound, http.StatusGone, http.StatusServiceUnavailable} {
		status = code
		subscribePush(t, a, cookie, csrf, sub, "zh-CN")
		s.db.Exec(`UPDATE web_push_subscriptions SET last_test_at=0`)
		if w := apiRequest(a, "POST", "/api/push/test", pushEndpointBody(sub), cookie, csrf); w.Code != 502 {
			t.Fatalf("provider error reported as success: %d", w.Code)
		}
		var remaining int
		s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&remaining)
		if (remaining == 0) != (code == 404 || code == 410) {
			t.Fatalf("bad pruning for provider status %d", code)
		}
	}
}

func TestPushRecipientsAndRevokedAccess(t *testing.T) {
	s := testStore(t)
	alice, bob, carol := testUser(t, s, "alice"), testUser(t, s, "bob"), testUser(t, s, "carol")
	p := testProject(t, s, alice, bob, carol)
	p.SharedChats = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	s.db.Exec(`INSERT INTO chat_collaborators VALUES(?,?)`, chat.ID, bob.ID)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	var sent []string
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, r.URL.String())
		return pushResponse(201), nil
	})}
	for _, u := range []User{alice, bob, carol} {
		cookie, csrf := loginRequest(t, a, u.Username)
		sub, _ := testPushSubscription(t, u.Username)
		subscribePush(t, a, cookie, csrf, sub, "en")
	}
	job := pushJob{chatID: chat.ID, turnID: "turn-12345678", kind: "completed"}
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 2 || strings.Contains(strings.Join(sent, " "), "carol") {
		t.Fatalf("shared project notified non-participants: %v", sent)
	}
	sent = nil
	s.db.Exec(`DELETE FROM project_members WHERE project_id=? AND user_id=?`, p.ID, bob.ID)
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 1 || !strings.HasSuffix(sent[0], "alice") {
		t.Fatalf("revoked member received push: %v", sent)
	}
	sent = nil
	s.db.Exec(`UPDATE auth_sessions SET expires_at=0 WHERE user_id=?`, alice.ID)
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 0 {
		t.Fatal("expired login received push")
	}
	job.kind, job.approvalID = "approval", "obsolete-approval"
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 0 {
		t.Fatal("obsolete approval received push")
	}
}

func TestPushApprovalRequiresCurrentControlAndTurnParticipants(t *testing.T) {
	s := testStore(t)
	alice, bob, carol := testUser(t, s, "alice"), testUser(t, s, "bob"), testUser(t, s, "carol")
	p := testProject(t, s, alice, bob, carol)
	p.SharedChats = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	turn, err := s.beginTurnBy(chat.ID, "test", chatmodel.BackendCodex, "", nil, carol.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		text string
		args []any
	}{
		{`INSERT INTO chat_collaborators VALUES(?,?)`, []any{chat.ID, bob.ID}},
		{`INSERT INTO project_member_own_access VALUES(?,?,'read-only') ON CONFLICT(project_id,user_id) DO UPDATE SET access='read-only'`, []any{p.ID, alice.ID}},
		{`INSERT INTO project_member_access VALUES(?,?,'read-only') ON CONFLICT(project_id,user_id) DO UPDATE SET access='read-only'`, []any{p.ID, carol.ID}},
	} {
		if _, err := s.db.Exec(query.text, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	var sent []string
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, r.URL.String())
		return pushResponse(201), nil
	})}
	for _, u := range []User{alice, bob, carol} {
		cookie, csrf := loginRequest(t, a, u.Username)
		sub, _ := testPushSubscription(t, u.Username)
		subscribePush(t, a, cookie, csrf, sub, "en")
	}
	job := pushJob{chatID: chat.ID, turnID: turn.ID, kind: "completed"}
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 3 {
		t.Fatalf("owner, collaborator and turn submitter should receive completion: %v", sent)
	}
	sent = nil
	job.kind, job.approvalID = "approval", "pending-approval"
	a.approvals[job.approvalID] = &pendingApproval{chatID: chat.ID, turnID: turn.ID}
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 1 || !strings.HasSuffix(sent[0], "bob") {
		t.Fatalf("read-only participant received an actionable approval: %v", sent)
	}
	sent = nil
	a.approvals[job.approvalID].decided = true
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 0 {
		t.Fatal("resolved approval was delivered")
	}
	job.kind, job.queuedAt = "completed", time.Now().Add(-6*time.Minute)
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 0 {
		t.Fatal("stale queued notification was delivered")
	}
	job.queuedAt = time.Time{}
	s.db.Exec(`UPDATE users SET disabled=1`)
	a.deliverPushJob(context.Background(), job)
	if len(sent) != 0 {
		t.Fatal("disabled user was notified")
	}
}

func TestPushSubscriptionDeviceLimit(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	sub, _ := testPushSubscription(t, "device")
	for index := 0; index < 20; index++ {
		sub.Endpoint += "x"
		subscribePush(t, a, cookie, csrf, sub, "en")
	}
	// Refreshing one of the twenty devices is idempotent and remains allowed.
	subscribePush(t, a, cookie, csrf, sub, "ja")
	sub.Endpoint += "x"
	body, _ := json.Marshal(map[string]any{"subscription": sub, "language": "en"})
	if w := apiRequest(a, "POST", "/api/push/subscribe", string(body), cookie, csrf); w.Code != 409 {
		t.Fatalf("device limit was not enforced: %d", w.Code)
	}
}

func TestPushHooksAgentCompletionFailureAndStop(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		kind string
	}{{"completed", nil, "completed"}, {"failed", errors.New("private provider error"), "failed"}, {"stopped", context.Canceled, ""}} {
		t.Run(test.name, func(t *testing.T) {
			s := testStore(t)
			u := testUser(t, s, "alice")
			p := testProject(t, s, u)
			chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
			a := NewApp(s, &fakeRunner{text: "private assistant reply", err: test.err}, "", "")
			t.Cleanup(func() { a.Close() })
			cookie, csrf := loginRequest(t, a, u.Username)
			sub, private := testPushSubscription(t, test.name)
			subscribePush(t, a, cookie, csrf, sub, "en")
			delivered := make(chan map[string]string, 2)
			a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
				delivered <- decryptPush(t, r, private, sub)
				return pushResponse(201), nil
			})}
			_, _ = a.runTurn(context.Background(), chat.ID, "private user prompt", nil)
			if test.kind == "" {
				a.push.mu.Lock()
				started := a.push.queue != nil
				a.push.mu.Unlock()
				if started {
					t.Fatal("manual stop queued a notification")
				}
				return
			}
			select {
			case payload := <-delivered:
				body, _ := json.Marshal(payload)
				if payload["url"] != "/?chat="+chat.ID || !strings.HasSuffix(payload["tag"], "-"+test.kind) || bytes.Contains(body, []byte("private")) {
					t.Fatalf("wrong/private notification payload: %s", body)
				}
				expectedTitle := "Spool - Task succeeded"
				if test.kind == "failed" {
					expectedTitle = "Spool - Task failed"
				}
				if payload["title"] != expectedTitle {
					t.Fatalf("task status missing from notification title: %q", payload["title"])
				}
			case <-time.After(3 * time.Second):
				t.Fatal("agent event did not trigger a push")
			}
		})
	}
}

type pushApprovalRunner struct{}

func (*pushApprovalRunner) Binary(chatmodel.Backend) (string, error) { return "/fake", nil }
func (*pushApprovalRunner) Run(ctx context.Context, request agent.AgentRequest, _ func(agent.AgentEvent) error) (agent.AgentResult, error) {
	_, err := request.Approve(ctx, chatmodel.ApprovalRequest{})
	return agent.AgentResult{}, err
}

func TestPushApprovalHookAndShutdown(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &pushApprovalRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	sub, private := testPushSubscription(t, "approval")
	subscribePush(t, a, cookie, csrf, sub, "en")
	started := make(chan struct{})
	var canceled atomic.Bool
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		payload := decryptPush(t, r, private, sub)
		if !strings.HasSuffix(payload["tag"], "-approval") || payload["title"] != "Spool - Approval required" {
			t.Error("approval did not produce approval push")
		}
		close(started)
		<-r.Context().Done()
		canceled.Store(true)
		return nil, r.Context().Err()
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runTurn(context.Background(), chat.ID, "approval needed", nil)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("approval was not pushed")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	if !canceled.Load() {
		t.Fatal("shutdown did not cancel and wait for active push")
	}
}

func TestPushTaskTitlesAreLocalized(t *testing.T) {
	a := &App{}
	for _, test := range []struct {
		language, kind, title string
	}{
		{"en", "completed", "Spool - Task succeeded"},
		{"en", "failed", "Spool - Task failed"},
		{"en", "approval", "Spool - Approval required"},
		{"zh-CN", "completed", "Spool · 任务成功"},
		{"zh-CN", "failed", "Spool · 任务失败"},
		{"zh-CN", "approval", "Spool · 需要审核"},
		{"ja", "completed", "Spool · タスク成功"},
		{"ja", "failed", "Spool · タスク失敗"},
		{"ja", "approval", "Spool · 承認が必要"},
	} {
		t.Run(test.language+"/"+test.kind, func(t *testing.T) {
			title, body := a.pushNotificationText(test.language, pushJob{kind: test.kind})
			if title != test.title || body == "" {
				t.Fatalf("unexpected task text: %q %q", title, body)
			}
		})
	}
}

func TestPushDisplayNameIsSingleLineAndBounded(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"  Alice\r\n\tSmith  ", "Alice Smith"},
		{"\x00\u202eAlice\u2069", "Alice"},
		{"\x00\u202e\u2069", ""},
		{strings.Repeat("界", 81), strings.Repeat("界", 77) + "..."},
		{"Alice {name}", "Alice {name}"},
	} {
		if got := pushDisplayName(test.value); got != test.want {
			t.Errorf("pushDisplayName(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}
