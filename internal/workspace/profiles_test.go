package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	chatmodel "spool/internal/model"
)

func TestProfilesAreAuthenticatedScopedAndSeparateFromChats(t *testing.T) {
	s := testStore(t)
	alice, bob, private := testUser(t, s, "alice"), testUser(t, s, "bob"), testUser(t, s, "private")
	project := testProject(t, s, alice, bob)
	project.SharedChats = true
	if _, err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(bob.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, alice.Username)
	if w := apiRequest(app, "GET", "/api/profiles", "", nil, ""); w.Code != 401 {
		t.Fatal("anonymous profile read accepted")
	}
	read := func() ProfileSnapshot {
		t.Helper()
		w := apiRequest(app, "GET", "/api/profiles", "", cookie, csrf)
		var value ProfileSnapshot
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), private.ID) || strings.Contains(w.Body.String(), "username") || strings.Contains(w.Body.String(), "disabled") {
			t.Fatal("profile response leaked unrelated or private account fields", w.Body.String())
		}
		return value
	}
	before := read()
	if len(before.People) != 2 {
		t.Fatalf("missing authorized operator: %+v", before)
	}
	if err = s.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "latest"}); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after.Revision <= before.Revision {
		t.Fatal("profile edit did not advance revision")
	}
	for _, person := range after.People {
		if person.ID == bob.ID && person.Avatar.Seed != "latest" {
			t.Fatal("avatar did not update")
		}
	}
	for _, path := range []string{"/api/navigation", "/api/chats?project_id=" + project.ID, "/api/chats/" + chat.ID} {
		w := apiRequest(app, "GET", path, "", cookie, csrf)
		if w.Code != 200 || strings.Contains(w.Body.String(), `"avatar"`) || strings.Contains(w.Body.String(), `"nickname"`) {
			t.Fatal("chat payload contains mutable profile data", path, w.Code, w.Body.String())
		}
	}
	project.SharedChats = false
	if _, err = s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got.People) != 1 || got.People[0].ID != alice.ID {
		t.Fatal("revoked chat operator remained in profiles", got)
	}
	// Presence is already shared globally, but must not turn into a user directory.
	_, bobIdentity, err := s.login(bob.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	app.presence.Touch(bobIdentity, "device", true)
	if got := read(); len(got.People) != 2 {
		t.Fatal("online identity could not be resolved", got)
	}
	app.presence.Disconnect(bob.ID, "device")
	if got := read(); len(got.People) != 1 {
		t.Fatal("offline unrelated identity remained visible", got)
	}
}

func TestProfileRevisionPersistsAndRenamesAreIndependentOfChatActivity(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	project := testProject(t, s, alice, bob)
	chat, err := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.beginTurnBy(chat.ID, "hello", chat.Backend, "", nil, bob.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.userProfiles(alice.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	navBefore, _ := s.UserNavigation(alice.ID)
	if _, err = s.saveUserWithPasswordCost(UserInput{ID: bob.ID, Username: bob.Username, Nickname: "Robert"}, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "chosen"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAvatar(bob.ID, Avatar{Style: "invalid"}); err == nil {
		t.Fatal("invalid avatar accepted")
	}
	navAfter, _ := s.UserNavigation(alice.ID)
	if !reflect.DeepEqual(navBefore, navAfter) {
		t.Fatal("profile edits changed chat membership or activity")
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.userProfiles(alice.ID, nil)
	if err != nil || after.Revision != before.Revision+2 {
		t.Fatal("revision was not transactional/durable", before.Revision, after.Revision, err)
	}
	for _, person := range after.People {
		if person.ID == bob.ID && (person.Nickname != "Robert" || person.Avatar.Seed != "chosen") {
			t.Fatal("profile edit missing", person)
		}
	}
}

func TestChatSSEParticipantsCarryOnlyIdentityIDs(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	project := testProject(t, s, alice, bob)
	chat, err := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.beginTurnBy(chat.ID, "hello", chat.Backend, "", nil, bob.ID); err != nil {
		t.Fatal(err)
	}
	app := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, _ := loginRequest(t, app, alice.Username)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	check := func(event string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case frame := <-stream.writes:
				if strings.Contains(string(frame), `"avatar"`) || strings.Contains(string(frame), `"nickname"`) {
					t.Fatal("SSE contains mutable profile data", string(frame))
				}
				prefix := "event: " + event + "\ndata: "
				if !strings.HasPrefix(string(frame), prefix) {
					continue
				}
				var value struct {
					Participants []map[string]any `json:"participants"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(string(frame), prefix)), &value); err != nil {
					t.Fatal(err)
				}
				if len(value.Participants) != 1 || len(value.Participants[0]) != 1 || value.Participants[0]["id"] != bob.ID {
					t.Fatal("SSE lost operator identity", string(frame))
				}
				return
			case <-timer.C:
				t.Fatal("missing SSE", event)
			}
		}
	}
	check("snapshot")
	if err = s.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "new-profile"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET title='new title',updated_at=updated_at+1 WHERE id=?`, chat.ID); err != nil {
		t.Fatal(err)
	}
	check("metadata")
}
