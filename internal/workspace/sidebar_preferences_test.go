package workspace

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/coder/websocket"

	chatmodel "spool/internal/model"
)

func TestSidebarPreferencesPersistMergeAndStayScoped(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	first, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	second, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendClaude)
	private, _ := s.CreateChat(bob.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, alice.Username)
	post := func(body string) SidebarPreferences {
		t.Helper()
		w := apiRequest(a, "POST", "/api/sidebar-preferences", body, cookie, csrf)
		var result SidebarPreferences
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("sidebar update: %d %s", w.Code, w.Body.String())
		}
		return result
	}
	initial := fmt.Sprintf(`{"initial":{"pinned":[%q,%q,%q]}}`, first.ID, private.ID, first.ID)
	got := post(initial)
	if got.Revision != 1 || !reflect.DeepEqual(got.Pinned, []string{first.ID}) {
		t.Fatalf("migration must deduplicate and authorize: %#v", got)
	}
	// A stale device's local cache cannot replace an initialized server record.
	if stale := post(`{"initial":{"pinned":[]}}`); !reflect.DeepEqual(stale, got) {
		t.Fatalf("stale migration overwrote server: %#v", stale)
	}
	got = post(fmt.Sprintf(`{"chat_id":%q,"pinned":true}`, second.ID))
	if got.Revision != 2 || !reflect.DeepEqual(got.Pinned, []string{second.ID, first.ID}) {
		t.Fatalf("unrelated changes were lost: %#v", got)
	}
	if retry := post(fmt.Sprintf(`{"chat_id":%q,"pinned":true}`, first.ID)); !reflect.DeepEqual(retry, got) {
		t.Fatalf("retry reordered an existing pin: %#v", retry)
	}
	for _, body := range []string{
		`{"user_id":"` + bob.ID + `","section":"pinned","collapsed":true}`,
		`{"section":"unknown","collapsed":true}`,
		`{"section":"pinned","collapsed":true,"pinned":true,"chat_id":"x"}`,
		`{"initial":{"pinned":[],"collapsed":{"unknown":true}}}`,
		`{}`,
	} {
		if w := apiRequest(a, "POST", "/api/sidebar-preferences", body, cookie, csrf); w.Code != 400 {
			t.Fatalf("invalid change accepted: %s: %d", body, w.Code)
		}
	}
	if w := apiRequest(a, "POST", "/api/sidebar-preferences", fmt.Sprintf(`{"chat_id":%q,"pinned":true}`, private.ID), cookie, csrf); w.Code != 403 {
		t.Fatalf("unauthorized pin accepted: %d", w.Code)
	}
	if w := apiRequest(a, "POST", "/api/sidebar-preferences", `{"section":"pinned","collapsed":true}`, cookie, "bad"); w.Code != 403 {
		t.Fatal("sidebar update bypassed CSRF")
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	saved, err := readSidebarPreferences(other.db, alice.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("server restart lost sidebar: %#v %v", saved, err)
	}
	untouched, err := readSidebarPreferences(other.db, bob.ID)
	if err != nil || untouched.Revision != 0 || len(untouched.Pinned) != 0 {
		t.Fatalf("account settings leaked: %#v %v", untouched, err)
	}
	w := apiRequest(a, "GET", "/api/navigation", "", cookie, csrf)
	var nav Navigation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &nav) != nil || !reflect.DeepEqual(nav.Sidebar, got) {
		t.Fatalf("navigation did not restore preferences: %s", w.Body.String())
	}
	got = post(fmt.Sprintf(`{"chat_id":%q,"pinned":false}`, first.ID))
	if !reflect.DeepEqual(got.Pinned, []string{second.ID}) {
		t.Fatalf("unpin changed unrelated data: %#v", got)
	}
}

func TestSidebarPreferencesConcurrentDevicesKeepIndependentChanges(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	p := testProject(t, s, alice)
	ids := []string{}
	for range 10 {
		chat, err := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, chat.ID)
	}
	var wg sync.WaitGroup
	on := true
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ChangeSidebarPreferences(alice.ID, SidebarPreferenceChange{ChatID: id, Pinned: &on}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := readSidebarPreferences(s.db, alice.ID)
	if err != nil || got.Revision != 10 || len(got.Pinned) != 10 {
		t.Fatalf("concurrent updates lost: %#v %v", got, err)
	}
	for _, id := range ids {
		if !slices.Contains(got.Pinned, id) {
			t.Fatalf("missing pin %s", id)
		}
	}
}

func TestSidebarPreferencesRealtimeUpdatesOtherDevicesWithoutLeakingAccounts(t *testing.T) {
	a, server, alice, project, cookie, csrf := realtimeFixture(t)
	bob := testUser(t, a.store, "bob")
	otherCookie, otherCSRF := loginRequest(t, a, bob.Username)
	chat, _ := a.store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	first := realtimeConnect(t, server, cookie, csrf, true)
	second := realtimeConnect(t, server, cookie, csrf, true)
	other := realtimeConnect(t, server, otherCookie, otherCSRF, true)
	realtimeReady(t, first)
	realtimeReady(t, second)
	realtimeReady(t, other)
	w := apiRequest(a, "POST", "/api/sidebar-preferences", fmt.Sprintf(`{"chat_id":%q,"pinned":true}`, chat.ID), cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, conn := range []*websocket.Conn{first, second} {
		message := realtimeUntil(t, conn, func(m realtimeMessage) bool {
			var nav Navigation
			return m.Type == "navigation" && json.Unmarshal(m.Data, &nav) == nil && nav.Sidebar.Revision == 1
		})
		var nav Navigation
		json.Unmarshal(message.Data, &nav)
		if !reflect.DeepEqual(nav.Sidebar.Pinned, []string{chat.ID}) {
			t.Fatalf("other device missed pin: %s", message.Data)
		}
	}
	// Cause Bob's own navigation to change so we can observe his authorized
	// snapshot even when unchanged payloads are deduplicated by the stream.
	w = apiRequest(a, "POST", "/api/sidebar-preferences", `{"initial":{"pinned":[]}}`, otherCookie, otherCSRF)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	message := realtimeUntil(t, other, func(m realtimeMessage) bool {
		var nav Navigation
		return m.Type == "navigation" && json.Unmarshal(m.Data, &nav) == nil && nav.Sidebar.Revision == 1
	})
	var nav Navigation
	json.Unmarshal(message.Data, &nav)
	if len(nav.Sidebar.Pinned) != 0 {
		t.Fatalf("sidebar leaked between accounts: %s", message.Data)
	}
}
