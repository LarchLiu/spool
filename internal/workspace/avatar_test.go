package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAvatarValidation(t *testing.T) {
	for _, style := range []string{"clay", "pixel-art", "croodles", "lorelei", "notionists", "toon-head", "voxel-art", "thumbs"} {
		for _, seed := range []string{"", "Alice:12", "小明", strings.Repeat("x", 128), strings.Repeat("猫", 42)} {
			if err := (Avatar{Style: style, Seed: seed}).Validate(); err != nil {
				t.Fatalf("valid avatar rejected: %q %q: %v", style, seed, err)
			}
		}
	}
	for _, avatar := range []Avatar{
		{}, {Style: "https://example.com/avatar.svg"}, {Style: "<svg/>"},
		{Style: "bottts-neutral"}, {Style: "shapes"}, {Style: "initials"},
		{Style: "thumbs", Seed: strings.Repeat("x", 129)},
		{Style: "thumbs", Seed: strings.Repeat("猫", 43)},
		{Style: "thumbs", Seed: " \t"}, {Style: "thumbs", Seed: "a\nb"},
		{Style: "thumbs", Seed: "\x00"}, {Style: "thumbs", Seed: "\xff"},
	} {
		if err := avatar.Validate(); err == nil {
			t.Fatalf("invalid avatar accepted: %#v", avatar)
		}
	}
}

func TestAvatarAPIIsAuthenticatedScopedAndPersists(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	if w := apiRequest(a, "POST", "/api/me/avatar", `{"style":"thumbs","seed":"alice:1"}`, nil, ""); w.Code != 401 {
		t.Fatal("anonymous avatar write accepted")
	}
	cookie, csrf := loginRequest(t, a, alice.Username)
	me := apiRequest(a, "GET", "/api/me", "", cookie, csrf)
	var response struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &response); err != nil || response.User.Avatar != DefaultAvatar() {
		t.Fatalf("missing automatic avatar: %s", me.Body.String())
	}
	body := `{"style":"thumbs","seed":"alice:1"}`
	if w := apiRequest(a, "POST", "/api/me/avatar", body, cookie, "bad"); w.Code != 403 {
		t.Fatal("avatar write bypassed CSRF")
	}
	r := httptest.NewRequest("POST", "/api/me/avatar", strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", "https://attacker.invalid")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin avatar write accepted")
	}
	for _, invalid := range []string{
		`{"style":"thumbs","seed":"x","user_id":"` + bob.ID + `"}`,
		`{"style":"https://example.com/a.svg","seed":"x"}`,
		`{"style":"thumbs","seed":"x","svg":"<svg/>"}`,
		`{"style":"thumbs","seed":"` + strings.Repeat("a", 129) + `"}`,
		`{"style":"thumbs","seed":"\n"}`, `{}`, `null`, body + body,
	} {
		if w := apiRequest(a, "POST", "/api/me/avatar", invalid, cookie, csrf); w.Code != 400 {
			t.Fatalf("invalid avatar accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, "POST", "/api/me/avatar", body, cookie, csrf)
	want := Avatar{Style: "thumbs", Seed: "alice:1"}
	var updated User
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.ID != alice.ID || updated.Avatar != want {
		t.Fatalf("avatar not saved: %d %s", w.Code, w.Body.String())
	}
	// Avatar updates are profile changes, not credential changes: keep sessions valid.
	me = apiRequest(a, "GET", "/api/me", "", cookie, csrf)
	if me.Code != 200 || json.Unmarshal(me.Body.Bytes(), &response) != nil || response.User.Avatar != want {
		t.Fatalf("avatar not restored by authentication: %s", me.Body.String())
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	users, err := reopened.Users()
	if err != nil || len(users) != 2 || users[0].Avatar != want || users[1].Avatar != DefaultAvatar() {
		t.Fatalf("avatar persistence/isolation: %#v %v", users, err)
	}
	// Account edits must preserve a hand-picked avatar, including its seed.
	renamed, err := s.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: "New name"})
	if err != nil || renamed.Avatar != want {
		t.Fatalf("account edit lost avatar: %#v %v", renamed, err)
	}
	cookie, csrf = loginRequest(t, a, alice.Username)
	w = apiRequest(a, "POST", "/api/me/avatar", `{"style":"clay","seed":""}`, cookie, csrf)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.Avatar != DefaultAvatar() {
		t.Fatalf("could not restore automatic avatar: %s", w.Body.String())
	}
}

func TestPresenceUsesCurrentAvatarWithoutAnotherHeartbeat(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	_, id, err := s.login(u.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	p := &Presence{}
	p.Touch(id, "browser", true)
	want := Avatar{Style: "voxel-art", Seed: "chosen:42"}
	if err := s.SaveAvatar(u.ID, want); err != nil {
		t.Fatal(err)
	}
	people := p.List(s)
	if len(people) != 1 || people[0].Avatar != want {
		t.Fatalf("presence returned stale avatar: %#v", people)
	}
}

func TestAvatarTableAddedToExistingVersionOneStore(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "existing")
	if _, err := s.db.Exec(`DROP TABLE user_avatars`); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	users, err := reopened.Users()
	if err != nil || len(users) != 1 || users[0].ID != u.ID || users[0].Avatar != DefaultAvatar() {
		t.Fatalf("existing user migration failed: %#v %v", users, err)
	}
	if err := reopened.SaveAvatar(u.ID, Avatar{Style: "lorelei", Seed: ""}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SaveAvatar("not-an-id", DefaultAvatar()); err == nil {
		t.Fatal("invalid user accepted")
	}
	if err := reopened.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SaveAvatar(u.ID, DefaultAvatar()); err == nil {
		t.Fatal("disabled user avatar write accepted")
	}
}

func TestAvatarAPIAcceptsAllEightStyles(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "eight-styles")
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	for _, style := range []string{"clay", "pixel-art", "croodles", "lorelei", "notionists", "toon-head", "voxel-art", "thumbs"} {
		body := `{"style":"` + style + `","seed":"selected:8"}`
		w := apiRequest(a, "POST", "/api/me/avatar", body, cookie, csrf)
		var updated User
		want := Avatar{Style: style, Seed: "selected:8"}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.Avatar != want {
			t.Fatalf("could not save %s: %d %s", style, w.Code, w.Body.String())
		}
		me := apiRequest(a, "GET", "/api/me", "", cookie, csrf)
		var response struct {
			User User `json:"user"`
		}
		if me.Code != 200 || json.Unmarshal(me.Body.Bytes(), &response) != nil || response.User.Avatar != want {
			t.Fatalf("could not restore %s: %d %s", style, me.Code, me.Body.String())
		}
	}
}

func TestRetiredAvatarStylesMigrateToClayWithoutLosingSeeds(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "legacy-avatar")
	token, _, err := s.login(u.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"bottts-neutral", "shapes", "initials", "thumbs"} {
		for _, seed := range []string{"", "picked:42"} {
			if _, err := s.db.Exec(`INSERT INTO user_avatars VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET style=excluded.style,seed=excluded.seed`, u.ID, style, seed); err != nil {
				t.Fatal(err)
			}
			want := Avatar{Style: "clay", Seed: seed}
			if style == "thumbs" {
				want.Style = "thumbs"
			}
			// Repeated startup must be safe, including for unchanged Thumbs choices.
			for range 2 {
				reopened, err := OpenStore(s.Root())
				if err != nil {
					t.Fatal(err)
				}
				users, err := reopened.Users()
				if err != nil || len(users) != 1 || users[0].Avatar != want {
					reopened.Close()
					t.Fatalf("migration of %s (%q): %#v %v", style, seed, users, err)
				}
				identity, err := reopened.authenticate(token)
				reopened.Close()
				if err != nil || identity.User.Avatar != want {
					t.Fatalf("migration invalidated session or returned stale avatar: %#v %v", identity.User.Avatar, err)
				}
			}
		}
	}
}
