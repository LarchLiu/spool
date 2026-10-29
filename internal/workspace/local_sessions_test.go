package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func localSessionHomes(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
}

func writeLocalSession(t *testing.T, backend chatmodel.Backend, id, directory string, order int) string {
	t.Helper()
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "10", "29", "rollout-"+id+".jsonl")
	if backend == chatmodel.BackendClaude {
		path = filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "test-project", id+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 29, 0, 0, order, 0, time.UTC)
	if backend == chatmodel.BackendCodex {
		appendTitleRecord(t, path, map[string]any{"type": "session_meta", "timestamp": stamp.Format(time.RFC3339Nano), "payload": map[string]any{"id": id, "cwd": directory, "source": "cli"}})
		appendNativeTurns(t, path, 0, 1, 0)
	} else {
		appendTitleRecord(t, path, map[string]any{"type": "user", "uuid": "user-" + id, "sessionId": id, "cwd": directory, "timestamp": stamp.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "Local request " + id}})
		appendTitleRecord(t, path, map[string]any{"type": "assistant", "uuid": "reply-" + id, "parentUuid": "user-" + id, "sessionId": id, "timestamp": stamp.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "content": "Local response"}})
		appendTitleRecord(t, path, map[string]any{"type": "custom-title", "customTitle": "Local " + id})
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalSharingLatestTwentyMixedProvidersAndRefresh(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	paths := map[int]string{}
	for i := range 25 {
		backend := chatmodel.BackendCodex
		if i%2 == 1 {
			backend = chatmodel.BackendClaude
		}
		paths[i] = writeLocalSession(t, backend, fmt.Sprintf("local-native-%02d", i), p.Path, i)
	}
	// Neither private mode nor project-wide sharing alone publishes native chats.
	for _, shared := range []bool{false, true} {
		p.SharedChats = shared
		if _, err := s.SaveProject(p); err != nil {
			t.Fatal(err)
		}
		nav, err := s.UserNavigation(bob.ID)
		if err != nil || len(nav.Chats) != 0 {
			t.Fatalf("local sharing was not opt-in: %+v %v", nav, err)
		}
	}
	spool, err := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNativeSessionPath(spool.ID, "local-native-24", paths[24]); err != nil {
		t.Fatal(err)
	}
	writeLocalSession(t, chatmodel.BackendCodex, "unrelated-native", t.TempDir(), 50)
	p.SharedLocalChats = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	indexes, reads := countNativeHistoryReads(s)
	nav, err := s.UserNavigation(bob.ID)
	if err != nil || len(nav.Chats) != 21 || indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("discovery did not stay metadata-only or cap local chats: %d %v", len(nav.Chats), err)
	}
	var locals []chatmodel.Session
	for _, chat := range nav.Chats {
		if chat.LocalSession {
			if chat.NativeSessionID != "" || chat.CanControl == nil || !*chat.CanControl {
				t.Fatalf("native ID leaked or control projection missing: %+v", chat)
			}
			locals = append(locals, chat)
		}
	}
	if len(locals) != 20 || !strings.Contains(locals[0].Title, "23") || locals[19].Backend != chatmodel.BackendCodex {
		t.Fatalf("latest mixed-provider window incorrect: %+v", locals)
	}
	oldID := locals[19].ID
	firstID := locals[0].ID
	stored, ok := s.Get(firstID)
	if !ok || len(stored.Turns) != 1 || stored.Turns[0].Reply != "Local response" || stored.UserID != "" {
		t.Fatalf("local history was not read from its native file: %+v", stored)
	}
	// Appends update metadata and live history without changing the public ID.
	appendTitleRecord(t, paths[23], map[string]any{"type": "custom-title", "customTitle": "Renamed local"})
	writeLocalSession(t, chatmodel.BackendCodex, "newest-native-26", p.Path, 60)
	if err := s.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	sessions, err := s.UserSessions(bob.ID, p.ID)
	if err != nil || len(sessions) != 21 || s.CanAccessChat(bob.ID, oldID) {
		t.Fatal("window did not rotate or old local chat remained addressable", err)
	}
	found := false
	for _, chat := range sessions {
		if chat.ID == firstID {
			found = chat.Title == "Renamed local"
		}
	}
	if !found {
		t.Fatal("native rename changed identity or did not sync")
	}
	outsider := testUser(t, s, "outsider")
	if s.CanAccessChat(outsider.ID, firstID) {
		t.Fatal("non-member accessed a local session")
	}
	// Disable without a scan: previously shared IDs must be denied immediately.
	p.SharedLocalChats = false
	if _, err := s.SaveProject(p); err != nil || s.CanAccessChat(bob.ID, firstID) {
		t.Fatal("disabling local sharing retained access", err)
	}
	p.SharedLocalChats = true
	p.SharedChats = false
	if _, err := s.SaveProject(p); err != nil || s.CanAccessChat(bob.ID, firstID) {
		t.Fatal("disabling project sharing retained local access", err)
	}
	assertNoTurnTables(t, s)
}

func TestLocalSharingMatchesLinkedWorktreesAndProtectsNestedProjects(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	git := filepath.Join(p.Path, ".git")
	gitWorktree := filepath.Join(git, "worktrees", "issue-42")
	if err := os.MkdirAll(gitWorktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitWorktree, "commondir"), []byte("../..\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitWorktree+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nestedPath := filepath.Join(p.Path, "private-project")
	if err := os.MkdirAll(nestedPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProject(Project{Name: "Private nested project", Path: nestedPath}); err != nil {
		t.Fatal(err)
	}
	writeLocalSession(t, chatmodel.BackendCodex, "private-nested-session", nestedPath, 40)
	writeLocalSession(t, chatmodel.BackendCodex, "external-worktree-session-42", worktree, 41)
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 1 || !nav.Chats[0].LocalSession {
		t.Fatalf("linked worktree not shared or nested private project leaked: %+v %v", nav, err)
	}
	chat, ok := s.getSession(nav.Chats[0].ID)
	directory, err := s.sessionDirectory(chat)
	if !ok || err != nil || directory != worktree {
		t.Fatal("linked worktree context was not preserved", directory, err)
	}
	testAgent(t, s)
	runner := &historyRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, u.Username)
	w := apiRequest(a, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
	if w.Code != 200 || strings.Contains(w.Body.String(), worktree) || strings.Contains(w.Body.String(), chat.NativeSessionID) || !strings.Contains(w.Body.String(), `"local_session":true`) {
		t.Fatalf("local API leaked paths or native identity: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"Continue the issue"}`, cookie, csrf)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"can_control":true`) {
		t.Fatalf("operator could not resume local issue session: %d %s", w.Code, w.Body.String())
	}
	a.turns.Wait()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) != 1 || runner.requests[0].WorkingDirectory != worktree || runner.requests[0].NativeSessionID != "external-worktree-session-42" {
		t.Fatalf("local session resumed in the wrong context: %+v", runner.requests)
	}
}

func TestProjectReadOnlyOnlyRestrictsSharedAccessNotOwnChatsOrAssignedTasks(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	p.SharedChats, p.SharedLocalChats = true, true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	shared, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	own, _ := s.CreateChat(bob.ID, p.ID, chatmodel.BackendCodex)
	task, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	if _, err := s.db.Exec(`INSERT INTO automation_chats VALUES(?,?)`, task.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO chat_collaborators VALUES(?,?)`, task.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	writeLocalSession(t, chatmodel.BackendCodex, "readonly-native-session", p.Path, 1)
	nav, err := s.UserNavigation(bob.ID)
	if err != nil || len(nav.Chats) != 4 {
		t.Fatal("local chat missing", err)
	}
	for _, chat := range nav.Chats {
		if !s.CanControlChat(bob.ID, chat.ID) || chat.CanControl == nil || !*chat.CanControl {
			t.Fatalf("operator lacks controls: %+v", chat)
		}
	}
	p.MemberAccess = map[string]string{bob.ID: "read-only"}
	p.Issues.UserID, p.Issues.AssigneeIDs = bob.ID, []string{bob.ID}
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, bob.Username)
	nav, err = s.UserNavigation(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range nav.Chats {
		allowed := chat.ID == own.ID || chat.ID == task.ID
		if !s.CanAccessChat(bob.ID, chat.ID) || s.CanControlChat(bob.ID, chat.ID) != allowed || chat.CanControl == nil || *chat.CanControl != allowed {
			t.Fatalf("readonly policy was not applied: chat=%s own=%s task=%s allowed=%v control=%v projection=%v", chat.ID, own.ID, task.ID, allowed, s.CanControlChat(bob.ID, chat.ID), chat.CanControl)
		}
		if !allowed {
			for _, suffix := range []string{"messages", "stop", "approvals", "goal", "fork"} {
				w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/"+suffix, `{}`, cookie, csrf)
				if w.Code != 404 {
					t.Fatalf("readonly mutation accepted at %s: %d", suffix, w.Code)
				}
			}
		}
		w := apiRequest(a, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
		var snapshot chatmodel.Session
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.CanControl == nil || *snapshot.CanControl != allowed {
			t.Fatal("readonly snapshot lacks explicit controls", w.Code, w.Body.String())
		}
	}
	if w := apiRequest(a, "POST", "/api/chats", `{"project_id":"`+p.ID+`"}`, cookie, csrf); w.Code != 201 {
		t.Fatal("shared readonly permission blocked creating a personal chat", w.Code)
	}
	testAgent(t, s)
	if w := apiRequest(a, "POST", "/api/chats/"+own.ID+"/messages", `{"message":"allowed by ownership"}`, cookie, csrf); w.Code != 202 {
		t.Fatalf("shared readonly permission blocked a personal message: %d %s", w.Code, w.Body.String())
	}
	a.turns.Wait()
	// Sidecar permissions survive reopening without changing legacy user rows.
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	projects, err := reopened.Projects()
	if err != nil || len(projects) != 1 || projects[0].MemberAccess[bob.ID] != "read-only" {
		t.Fatal("project permission did not persist", err)
	}
	if !reopened.CanControlChat(bob.ID, own.ID) || !reopened.CanControlChat(bob.ID, task.ID) || !reopened.CanAccessChat(bob.ID, shared.ID) || reopened.CanControlChat(bob.ID, shared.ID) {
		t.Fatal("reopened store lost readonly enforcement")
	}
}

func TestProjectMemberAccessIsScopedAndRemovedWithMembership(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	readOnly, writable := testProject(t, s, alice, bob), testProject(t, s, alice, bob)
	readOnly.SharedChats, writable.SharedChats = true, true
	readOnly.MemberAccess[bob.ID] = "read-only"
	for _, p := range []Project{readOnly, writable} {
		if _, err := s.SaveProject(p); err != nil {
			t.Fatal(err)
		}
	}
	view, _ := s.CreateChat(alice.ID, readOnly.ID, chatmodel.BackendCodex)
	control, err := s.CreateChat(bob.ID, writable.ID, chatmodel.BackendCodex)
	if err != nil || !s.CanControlChat(bob.ID, control.ID) || s.CanControlChat(bob.ID, view.ID) {
		t.Fatal("project permissions leaked between projects", err)
	}
	ownReadOnly, err := s.CreateChat(bob.ID, readOnly.ID, chatmodel.BackendCodex)
	if err != nil || !s.CanControlChat(bob.ID, ownReadOnly.ID) {
		t.Fatal("shared readonly permission blocked personal chats", err)
	}
	sharedWritable, err := s.CreateChat(alice.ID, writable.ID, chatmodel.BackendCodex)
	if err != nil || !s.CanControlChat(bob.ID, sharedWritable.ID) {
		t.Fatal("writable permission did not apply to another member's shared chat", err)
	}
	nav, err := s.UserNavigation(bob.ID)
	if err != nil || len(nav.Projects) != 2 {
		t.Fatal("missing projects", err)
	}
	for _, chat := range nav.Chats {
		if chat.CanControl == nil || *chat.CanControl != (chat.ID != view.ID) {
			t.Fatalf("incorrect batch permission: %+v", chat)
		}
	}
	readOnly.Members = []string{alice.ID}
	if _, err := s.SaveProject(readOnly); err != nil || s.CanAccessChat(bob.ID, view.ID) {
		t.Fatal("removed member retained access", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM project_member_access WHERE project_id=? AND user_id=?`, readOnly.ID, bob.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("removed membership retained permission", err)
	}
	readOnly.Members = append(readOnly.Members, bob.ID)
	delete(readOnly.MemberAccess, bob.ID)
	if _, err := s.SaveProject(readOnly); err != nil || !s.CanControlChat(bob.ID, view.ID) {
		t.Fatal("re-added member did not use explicit default control", err)
	}
	readOnly.MemberAccess[bob.ID] = "invalid"
	if _, err := s.SaveProject(readOnly); err == nil || !s.CanControlChat(bob.ID, view.ID) {
		t.Fatal("invalid permission was accepted or changed the saved permission")
	}
}

func TestOwnSpoolChatPermissionsAreIndependentPerProjectAndFromSharing(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p, other := testProject(t, s, alice, bob), testProject(t, s, alice, bob)
	p.SharedChats = true
	own, _ := s.CreateChat(bob.ID, p.ID, chatmodel.BackendCodex)
	shared, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	testAgent(t, s)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, bob.Username)
	for _, ownAccess := range []string{"read-only", "create"} {
		for _, sharedAccess := range []string{"read-only", "control"} {
			t.Run(ownAccess+"-"+sharedAccess, func(t *testing.T) {
				p.MemberOwnAccess[bob.ID] = ownAccess
				p.MemberAccess[bob.ID] = sharedAccess
				if _, err := s.SaveProject(p); err != nil {
					t.Fatal(err)
				}
				canCreate, canShare := ownAccess == "create", sharedAccess == "control"
				if s.CanCreateChat(bob.ID, p.ID) != canCreate || s.CanControlChat(bob.ID, own.ID) != canCreate || s.CanControlChat(bob.ID, shared.ID) != canShare {
					t.Fatal("own and shared permissions are not independent")
				}
				nav, err := s.UserNavigation(bob.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, project := range nav.Projects {
					if project.CanCreate == nil || *project.CanCreate != (project.ID != p.ID || canCreate) {
						t.Fatalf("incorrect per-project creation permission: %+v", project)
					}
				}
				for _, chat := range nav.Chats {
					allowed := canShare
					if chat.UserID == bob.ID {
						allowed = canCreate
					}
					if chat.CanControl == nil || *chat.CanControl != allowed {
						t.Fatalf("incorrect projected chat permission: %+v", chat)
					}
				}
				for _, chat := range []chatmodel.Session{own, shared} {
					allowed := canShare
					if chat.ID == own.ID {
						allowed = canCreate
					}
					if !s.CanAccessChat(bob.ID, chat.ID) {
						t.Fatal("read-only removed view access")
					}
					if !allowed {
						for _, suffix := range []string{"messages", "stop", "approvals", "goal", "fork"} {
							if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/"+suffix, `{}`, cookie, csrf); w.Code != 404 {
								t.Fatalf("read-only mutation accepted: %s %d", suffix, w.Code)
							}
						}
					} else {
						if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"allowed"}`, cookie, csrf); w.Code != 202 {
							t.Fatalf("authorized chat message rejected: %d %s", w.Code, w.Body.String())
						}
						a.turns.Wait()
					}
				}
				want := 403
				if canCreate {
					want = 201
				}
				if w := apiRequest(a, "POST", "/api/chats", `{"project_id":"`+p.ID+`"}`, cookie, csrf); w.Code != want {
					t.Fatalf("incorrect create permission: %d %s", w.Code, w.Body.String())
				}
				if !canCreate && canShare {
					if w := apiRequest(a, "POST", "/api/chats/"+shared.ID+"/fork", `{"backend":"claude"}`, cookie, csrf); w.Code != 403 {
						t.Fatal("shared control bypassed personal creation via conversion", w.Code)
					}
				}
				if !s.CanCreateChat(bob.ID, other.ID) {
					t.Fatal("personal permission leaked to another project")
				}
			})
		}
	}
	p.MemberOwnAccess[bob.ID] = "read-only"
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.CanCreateChat(bob.ID, p.ID) || reopened.CanControlChat(bob.ID, own.ID) || !reopened.CanCreateChat(bob.ID, other.ID) {
		t.Fatal("personal permissions did not persist independently")
	}
}

func TestSpoolOriginsIncludingIssueAutomationNeverConsumeLocalSlots(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	for i := range 25 {
		writeLocalSession(t, chatmodel.BackendCodex, fmt.Sprintf("external-%02d", i), p.Path, i)
		chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("spool-issue-%02d", i)
		path := writeLocalSession(t, chatmodel.BackendCodex, id, p.Path, 60+i)
		if err := s.SetNativeSessionPath(chat.ID, id, path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO automation_chats VALUES(?,?)`, chat.ID, p.ID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// A historical native ID remains Spool-origin after adoption changes.
			path = writeLocalSession(t, chatmodel.BackendCodex, "spool-issue-replaced", p.Path, 90)
			if err := s.SetNativeSessionPath(chat.ID, "spool-issue-replaced", path); err != nil {
				t.Fatal(err)
			}
		}
	}
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 45 {
		t.Fatalf("Spool automation consumed local slots: %d %v", len(nav.Chats), err)
	}
	localCount := 0
	for _, chat := range nav.Chats {
		if chat.LocalSession {
			localCount++
			stored, _ := s.getSession(chat.ID)
			if !strings.HasPrefix(stored.NativeSessionID, "external-") {
				t.Fatalf("Spool origin was imported as local: %+v", stored)
			}
		}
	}
	if localCount != 20 {
		t.Fatal("external session limit is not independent of issue automation", localCount)
	}
	// A duplicate discovered before Spool adopts its ID is hidden immediately,
	// without waiting for the next background discovery pass.
	var imported chatmodel.Session
	for _, chat := range nav.Chats {
		if chat.LocalSession {
			imported, _ = s.getSession(chat.ID)
			break
		}
	}
	spool, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err := s.SetNativeSession(spool.ID, imported.NativeSessionID); err != nil || s.CanAccessChat(u.ID, imported.ID) {
		t.Fatal("adopted duplicate remained visible", err)
	}
	if err := s.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	chats, err := s.UserSessions(u.ID, p.ID)
	localCount = 0
	for _, chat := range chats {
		if chat.LocalSession {
			localCount++
		}
	}
	if err != nil || localCount != 20 {
		t.Fatal("excluding an adopted ID did not refill the local window", localCount, err)
	}
}

func TestLocalSessionAppendRefreshesOpenStreamWithUnchangedMtime(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "viewer")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	path := writeLocalSession(t, chatmodel.BackendCodex, "external-stream", p.Path, 1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 1 {
		t.Fatal("missing local session", err)
	}
	chat := nav.Chats[0]
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, _ := loginRequest(t, a, u.Username)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	if event, initial := readChatStreamFrame(t, stream); event != "snapshot" || len(initial.Turns) != 1 {
		t.Fatal("local stream lacks initial native history", event)
	}
	appendNativeTurns(t, path, 1, 1, 0)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := s.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.getSession(chat.ID)
	if updated.UpdatedAt <= chat.UpdatedAt {
		t.Fatal("file generation did not advance SSE revision")
	}
	for range 8 {
		event, value := readChatStreamFrame(t, stream)
		if event == "turn" {
			if len(value.Turns) != 2 || len(value.Transcript.Parts) != 4 || value.Transcript.Parts[2].Text != "prompt-001" || value.UpdatedAt != updated.UpdatedAt {
				t.Fatalf("open stream did not refresh appended native history: %+v", value)
			}
			return
		}
	}
	t.Fatal("local append was never pushed to the open stream")
}
