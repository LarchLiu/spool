package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type preferencePermissionRunner struct {
	fakeRunner
	unavailable string
	fail        bool
	probes      int
}

func (r *preferencePermissionRunner) PermissionModes(ctx context.Context, backend chatmodel.Backend, directory string) (chatmodel.PermissionCatalog, error) {
	r.probes++
	if r.fail {
		return chatmodel.PermissionCatalog{}, errors.New("discovery failed")
	}
	catalog, err := r.fakeRunner.PermissionModes(ctx, backend, directory)
	for i := range catalog.Modes {
		if catalog.Modes[i].ID == r.unavailable {
			catalog.Modes[i].Available = false
		}
	}
	return catalog, err
}

func TestProjectPermissionChoiceAppliesAcrossChatsUsersAndRestarts(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p, other := testProject(t, s, alice, bob), testProject(t, s, alice)
	testAgent(t, s)
	if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendClaude, Enabled: true, Installed: true, Catalog: fixtureCatalog()}); err != nil {
		t.Fatal(err)
	}
	old, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	turn, err := s.BeginTurn(old.ID, "Original mode")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishTurn(old.ID, turn.ID, agent.AgentResult{Text: "Done"}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	runner := &preferencePermissionRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, alice.Username)
	bc, bs := loginRequest(t, a, bob.Username)
	path := "/api/permissions?project_id=" + p.ID + "&backend=codex"
	read := func(path string, cookie *http.Cookie, csrf, want string) {
		t.Helper()
		w := apiRequest(a, "GET", path, "", cookie, csrf)
		var catalog chatmodel.PermissionCatalog
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || catalog.Current != want {
			t.Fatalf("catalog: want %s: %d %s", want, w.Code, w.Body.String())
		}
	}
	read(path, cookie, csrf, "ask")
	// A menu choice is persisted before sending any message, including from an old chat.
	w := apiRequest(a, "POST", path+"&chat_id="+old.ID, `{"mode":"auto"}`, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	newChat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	for _, suffix := range []string{"", "&chat_id=" + old.ID, "&chat_id=" + newChat.ID} {
		read(path+suffix, cookie, csrf, "auto")
	}
	read(path, bc, bs, "auto")
	read("/api/permissions?project_id="+other.ID+"&backend=codex", cookie, csrf, "ask")
	read("/api/permissions?project_id="+p.ID+"&backend=claude", cookie, csrf, "acceptEdits")
	if w := apiRequest(a, "POST", "/api/permissions?project_id="+p.ID+"&backend=claude", `{"mode":"plan"}`, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	read(path, cookie, csrf, "auto")
	read("/api/permissions?project_id="+p.ID+"&backend=claude", cookie, csrf, "plan")
	// Sending an older chat's explicit mode must not replace the latest menu choice.
	if w := apiRequest(a, "POST", "/api/chats/"+old.ID+"/messages", `{"message":"Continue","permission_mode":"ask"}`, cookie, csrf); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitChat(t, s, old.ID)
	read(path+"&chat_id="+old.ID, cookie, csrf, "auto")
	if w := apiRequest(a, "POST", path, `{"mode":"ask"}`, bc, bs); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	read(path+"&chat_id="+newChat.ID, cookie, csrf, "ask")
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for backend, want := range map[chatmodel.Backend]string{chatmodel.BackendCodex: "ask", chatmodel.BackendClaude: "plan"} {
		mode, err := reopened.projectPermissionMode(p.ID, backend)
		if err != nil || mode != want {
			t.Fatalf("persisted %s: %q %v", backend, mode, err)
		}
	}
}

func TestProjectPermissionChoicePreservesDiscoveryAndAccessChecks(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p, private := testProject(t, s, alice, bob), testProject(t, s, alice)
	testAgent(t, s)
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	runner := &preferencePermissionRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, alice.Username)
	bc, bs := loginRequest(t, a, bob.Username)
	path := "/api/permissions?project_id=" + p.ID + "&backend=codex"
	for _, request := range []struct {
		path, body string
		cookie     *http.Cookie
		csrf       string
		status     int
	}{
		{path, `{"mode":"auto"}`, cookie, "bad", 403},
		{path, `{"mode":"auto"}`, nil, "", 401},
		{path, `{"mode":"bypassPermissions"}`, cookie, csrf, 400},
		{path, `{"mode":""}`, cookie, csrf, 400},
		{path, `{"mode":"auto","project_id":"other"}`, cookie, csrf, 400},
		{path + "&chat_id=" + chat.ID, `{"mode":"auto"}`, bc, bs, 404},
		{"/api/permissions?project_id=" + private.ID + "&backend=codex", `{"mode":"auto"}`, bc, bs, 404},
	} {
		if w := apiRequest(a, "POST", request.path, request.body, request.cookie, request.csrf); w.Code != request.status {
			t.Fatalf("invalid save: want %d: %d %s", request.status, w.Code, w.Body.String())
		}
	}
	p.MemberOwnAccess = map[string]string{bob.ID: "read-only"}
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "POST", path, `{"mode":"auto"}`, bc, bs); w.Code != 404 {
		t.Fatal("read-only user changed preference", w.Code, w.Body.String())
	}
	if mode, _ := s.projectPermissionMode(p.ID, chatmodel.BackendCodex); mode != "" {
		t.Fatal("rejected save created a preference")
	}
	if w := apiRequest(a, "POST", path, `{"mode":"auto"}`, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	runner.unavailable = "auto"
	before := runner.probes
	w := apiRequest(a, "GET", path, "", cookie, csrf)
	var catalog chatmodel.PermissionCatalog
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || catalog.Current != "auto" || catalog.Default != "auto" || catalog.Validate("auto") == nil || runner.probes != before+1 {
		t.Fatalf("saved mode bypassed discovery: %d %s", w.Code, w.Body.String())
	}
	for _, target := range []struct{ path, body string }{
		{path, `{"mode":"auto"}`},
		{"/api/chats/" + chat.ID + "/messages", `{"message":"Blocked","permission_mode":"auto"}`},
	} {
		if w := apiRequest(a, "POST", target.path, target.body, cookie, csrf); w.Code != 400 {
			t.Fatal("unavailable mode accepted", w.Code, w.Body.String())
		}
	}
	runner.fail = true
	for _, method := range []string{"GET", "POST"} {
		if w := apiRequest(a, method, path, `{"mode":"ask"}`, cookie, csrf); w.Code != 503 {
			t.Fatal("saved mode bypassed discovery failure", w.Code, w.Body.String())
		}
	}
	if mode, _ := s.projectPermissionMode(p.ID, chatmodel.BackendCodex); mode != "auto" {
		t.Fatal("failed save replaced preference")
	}
	if len(runner.requests) != 0 {
		t.Fatal("choosing permissions or rejected messages invoked the agent")
	}
}
