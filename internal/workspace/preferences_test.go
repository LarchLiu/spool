package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestUIPreferencesPersistAndStayScopedToUser(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	desktop := UIPreferences{Theme: "light", Language: "ja"}
	if err := s.SaveUIPreferences("", desktop); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, alice.Username)
	for _, body := range []string{`{"theme":"neon","language":"en"}`, `{"theme":"dark","language":"xx"}`, `{"theme":"dark","language":"en","user_id":"` + bob.ID + `"}`} {
		if w := apiRequest(a, "POST", "/api/preferences", body, cookie, csrf); w.Code != 400 {
			t.Fatalf("invalid preferences accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w := apiRequest(a, "POST", "/api/preferences", `{"theme":"dark","language":"en"}`, cookie, "bad"); w.Code != 403 {
		t.Fatal("preferences bypassed CSRF")
	}
	w := apiRequest(a, "POST", "/api/preferences", `{"theme":"dark","language":"en"}`, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	saved, _ := other.UIPreferences(alice.ID)
	untouched, _ := other.UIPreferences(bob.ID)
	local, _ := other.UIPreferences("")
	if saved != (UIPreferences{"dark", "en"}) || untouched != DefaultUIPreferences() || local != desktop {
		t.Fatalf("preferences scope/persistence: %#v %#v %#v", saved, untouched, local)
	}
	me := apiRequest(a, "GET", "/api/me", "", cookie, csrf)
	if !strings.Contains(me.Body.String(), `"preferences":{"theme":"dark","language":"en"}`) {
		t.Fatal("preferences were not restored with authentication")
	}
}

func TestGroupedNavigationOnlyContainsOwnedAuthorizedChats(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	p1, p2 := testProject(t, s, alice, bob), testProject(t, s, alice)
	first, _ := s.CreateChat(alice.ID, p1.ID, chatmodel.BackendCodex)
	second, _ := s.CreateChat(alice.ID, p2.ID, chatmodel.BackendClaude)
	s.CreateChat(bob.ID, p1.ID, chatmodel.BackendClaude)
	s.SetNativeSession(first.ID, "private-native-123")
	turn, _ := s.BeginTurn(first.ID, "Secret prompt")
	s.FinishTurn(first.ID, turn.ID, agent.AgentResult{Text: "Secret reply"}, nil, chatmodel.StatusCompleted)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, alice.Username)
	w := apiRequest(a, "GET", "/api/navigation", "", cookie, csrf)
	var navigation Navigation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &navigation) != nil {
		t.Fatal(w.Body.String())
	}
	ids := []string{}
	for _, chat := range navigation.Chats {
		ids = append(ids, chat.ID)
		if len(chat.Turns) != 0 {
			t.Fatal("navigation loaded conversation history")
		}
	}
	if len(navigation.Projects) != 2 || len(ids) != 2 || !slices.Contains(ids, first.ID) || !slices.Contains(ids, second.ID) {
		t.Fatalf("wrong navigation: %#v", navigation)
	}
	for _, secret := range []string{"private-native-123", `"prompt":`, "Secret reply", p1.Path} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("navigation leaked %q", secret)
		}
	}
	p2.Members = nil
	if _, err := s.SaveProject(p2); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/navigation", "", cookie, csrf)
	if strings.Contains(w.Body.String(), second.ID) {
		t.Fatal("navigation retained a revoked project")
	}
}

func TestLocalizedRequestErrors(t *testing.T) {
	a := NewApp(testStore(t), &fakeRunner{}, "", "")
	for language, message := range map[string]string{"en": "Please sign in", "zh-CN": "请先登录", "ja": "サインインしてください"} {
		r := httptest.NewRequest("GET", "/api/navigation", nil)
		r.Header.Set("X-UI-Language", language)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 401 || !strings.Contains(w.Body.String(), message) || w.Header().Get("Content-Language") != language {
			t.Fatalf("wrong translated error: %s %d %s", language, w.Code, w.Body.String())
		}
	}
}
