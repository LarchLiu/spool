package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestPushSubscriptionRestoresAfterLoginExpiryAndRestart(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	sub, private := testPushSubscription(t, "recovery")
	subscribePush(t, a, cookie, csrf, sub, "en")
	keys, err := s.webPushKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	a = NewApp(s, &fakeRunner{}, "", "")
	nextKeys, err := s.webPushKeys()
	if err != nil || keys != nextKeys {
		t.Fatal("restart changed VAPID keys", err)
	}
	if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), cookie, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatalf("restart lost subscription: %d %s", w.Code, w.Body.String())
	}
	sent := 0
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		sent++
		if title := decryptPush(t, r, private, sub)["title"]; title != "Spool · 任务成功："+chat.Title {
			t.Fatalf("refresh did not retain notification language and session title: %q", title)
		}
		return pushResponse(201), nil
	})}
	if _, err := s.db.Exec(`UPDATE auth_sessions SET expires_at=0 WHERE token_hash=?`, tokenHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	job := pushJob{chatID: chat.ID, turnID: "turn-12345678", kind: "completed"}
	a.deliverPushJob(context.Background(), job)
	if sent != 0 {
		t.Fatal("expired login received push")
	}
	// Logging in cleans expired sessions while preserving their device opt-ins.
	next, nextCSRF := loginRequest(t, a, u.Username)
	var owner string
	var session sql.NullString
	if err := s.db.QueryRow(`SELECT user_id,token_hash FROM web_push_subscriptions WHERE endpoint=?`, sub.Endpoint).Scan(&owner, &session); err != nil || owner != u.ID || session.Valid {
		t.Fatal("login cleanup lost or kept the expired binding", owner, session.Valid, err)
	}
	a.deliverPushJob(context.Background(), job)
	if sent != 0 {
		t.Fatal("unbound device received push before refresh")
	}
	refresh, _ := json.Marshal(map[string]string{"endpoint": sub.Endpoint, "language": "zh-CN"})
	if w := apiRequest(a, "POST", "/api/push/refresh", string(refresh), next, nextCSRF); w.Code != 200 {
		t.Fatalf("refresh failed: %d %s", w.Code, w.Body.String())
	}
	if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), next, nextCSRF); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatalf("relogin did not restore opt-in: %d %s", w.Code, w.Body.String())
	}
	a.deliverPushJob(context.Background(), job)
	if sent != 1 {
		t.Fatalf("restored device got %d notifications", sent)
	}
	// A new login can also replace a still-valid session on this device.
	newest, newestCSRF := loginRequest(t, a, u.Username)
	if w := apiRequest(a, "POST", "/api/push/refresh", string(refresh), newest, newestCSRF); w.Code != 200 {
		t.Fatal("could not rebind still-valid session", w.Code)
	}
	if w := apiRequest(a, "POST", "/api/logout", "{}", next, nextCSRF); w.Code != 200 {
		t.Fatal("old session logout failed", w.Code)
	}
	if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), newest, newestCSRF); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatal("old session logout revoked newer opt-in", w.Code, w.Body.String())
	}
}

func TestPushRefreshCannotReviveRevokedOrOtherAccountOptIns(t *testing.T) {
	for _, action := range []string{"disable", "logout", "account edit", "delete user"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
			a := NewApp(s, &fakeRunner{}, "", "")
			t.Cleanup(func() { a.Close() })
			cookie, csrf := loginRequest(t, a, alice.Username)
			other, otherCSRF := loginRequest(t, a, bob.Username)
			sub, _ := testPushSubscription(t, "revoked")
			subscribePush(t, a, cookie, csrf, sub, "en")
			refresh, _ := json.Marshal(map[string]string{"endpoint": sub.Endpoint, "language": "ja"})
			if w := apiRequest(a, "POST", "/api/push/refresh", string(refresh), other, otherCSRF); w.Code != 200 {
				t.Fatal("other-account refresh failed", w.Code)
			}
			if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), other, otherCSRF); w.Code != 200 || strings.Contains(w.Body.String(), "true") {
				t.Fatal("refresh took over another account's opt-in", w.Code, w.Body.String())
			}
			if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), cookie, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
				t.Fatal("other-account refresh changed owner's binding", w.Code, w.Body.String())
			}
			switch action {
			case "disable", "logout":
				path, body := "/api/push/unsubscribe", pushEndpointBody(sub)
				if action == "logout" {
					path, body = "/api/logout", "{}"
				}
				if w := apiRequest(a, "POST", path, body, cookie, csrf); w.Code != 200 {
					t.Fatal("could not revoke device", w.Code)
				}
			case "account edit":
				if _, err := s.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: "Renamed"}); err != nil {
					t.Fatal(err)
				}
			case "delete user":
				if err := s.DeleteUser(alice.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: alice.Nickname}); err != nil {
					t.Fatal(err)
				}
			}
			next, nextCSRF := loginRequest(t, a, alice.Username)
			if w := apiRequest(a, "POST", "/api/push/refresh", string(refresh), next, nextCSRF); w.Code != 200 {
				t.Fatal("refresh failed", w.Code)
			}
			if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), next, nextCSRF); w.Code != 200 || strings.Contains(w.Body.String(), "true") {
				t.Fatal("refresh revived revoked notifications", w.Code, w.Body.String())
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("revoked device opt-in survived", count, err)
			}
		})
	}
}

func TestPushSubscriptionMigrationPreservesExistingDevices(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	token, identity, err := s.login(u.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.webPushKeys()
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the released session-owned schema before opening the new store.
	if _, err := s.db.Exec(`DROP TABLE web_push_subscriptions;
CREATE TABLE web_push_subscriptions(endpoint TEXT PRIMARY KEY,token_hash TEXT NOT NULL REFERENCES auth_sessions(token_hash) ON DELETE CASCADE,p256dh TEXT NOT NULL,auth TEXT NOT NULL,language TEXT NOT NULL,updated_at INTEGER NOT NULL,last_test_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX web_push_session ON web_push_subscriptions(token_hash);`); err != nil {
		t.Fatal(err)
	}
	sub, _ := testPushSubscription(t, "migration")
	if _, err := s.db.Exec(`INSERT INTO web_push_subscriptions VALUES(?,?,?,?,?,?,?)`, sub.Endpoint, identity.TokenHash, sub.Keys.P256dh, sub.Keys.Auth, "ja", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	if err := migrateWebPushSchema(reopened.db); err != nil {
		t.Fatal("repeat migration failed", err)
	}
	a := NewApp(reopened, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie := &http.Cookie{Name: authCookie, Value: token}
	if w := apiRequest(a, "POST", "/api/push/status", pushEndpointBody(sub), cookie, identity.CSRF); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatal("migration lost enabled device", w.Code, w.Body.String())
	}
	var owner, binding, public, auth, language string
	var updated, tested int64
	if err := reopened.db.QueryRow(`SELECT user_id,token_hash,p256dh,auth,language,updated_at,last_test_at FROM web_push_subscriptions WHERE endpoint=?`, sub.Endpoint).Scan(&owner, &binding, &public, &auth, &language, &updated, &tested); err != nil {
		t.Fatal(err)
	}
	if owner != u.ID || binding != identity.TokenHash || public != sub.Keys.P256dh || auth != sub.Keys.Auth || language != "ja" || updated != 1234 || tested != 5678 {
		t.Fatal("migration changed subscription fields")
	}
	if next, err := reopened.webPushKeys(); err != nil || next != keys {
		t.Fatal("migration changed VAPID keys", err)
	}
	rows, err := reopened.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("migration left invalid foreign keys", rows.Err())
	}
}
