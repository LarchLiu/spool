package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type renameTestRunner struct {
	fakeRunner
	rename func(agent.AgentRequest, string) error
}

func (r *renameTestRunner) RenameSession(_ context.Context, request agent.AgentRequest, title string) error {
	return r.rename(request, title)
}

func TestRenameChatUpdatesNativeTitleAndNavigationWithoutActivity(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			owner := testUser(t, store, "owner")
			project := testProject(t, store, owner)
			chat, _ := titleSessionFixture(t, store, backend, owner.ID, project.ID)
			calls := 0
			runner := &renameTestRunner{rename: func(request agent.AgentRequest, title string) error {
				calls++
				if request.NativeSessionID != chat.NativeSessionID || request.NativeSessionPath != chat.NativeSessionPath || request.WorkingDirectory != project.Path || request.Backend != backend {
					t.Fatalf("incorrect rename target: %+v", request)
				}
				if backend == chatmodel.BackendClaude {
					appendTitleRecord(t, chat.NativeSessionPath, map[string]any{"type": "custom-title", "customTitle": title})
				} else {
					root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(chat.NativeSessionPath)))))
					appendTitleRecord(t, filepath.Join(root, "session_index.jsonl"), map[string]any{"id": chat.NativeSessionID, "thread_name": title})
				}
				return nil
			}}
			app := NewApp(store, runner, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, csrf := loginRequest(t, app, owner.Username)
			response := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/rename", `{"title":"  New custom title  "}`, cookie, csrf)
			var metadata chatMetadata
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &metadata) != nil || metadata.Title != "New custom title" || metadata.UpdatedAt != chat.UpdatedAt || calls != 1 {
				t.Fatalf("rename response: %d %s", response.Code, response.Body.String())
			}
			latest, _ := store.Get(chat.ID)
			if latest.Title != metadata.Title || latest.UpdatedAt != chat.UpdatedAt || latest.NativeSessionID != chat.NativeSessionID || len(latest.Turns) != 1 || len(latest.Transcript.Parts) != 2 {
				t.Fatalf("rename lost native identity or history: %+v", latest)
			}
			nav, err := store.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 1 || nav.Chats[0].Title != metadata.Title {
				t.Fatalf("sidebar rename was lost: %+v %v", nav, err)
			}
		})
	}
}

func TestRenameChatRejectsInvalidInputReadOnlyAndProviderFailure(t *testing.T) {
	store := testStore(t)
	owner, viewer := testUser(t, store, "owner"), testUser(t, store, "viewer")
	project := testProject(t, store, owner, viewer)
	project.SharedChats = true
	project.MemberAccess = map[string]string{viewer.ID: "read-only"}
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, _ := titleSessionFixture(t, store, chatmodel.BackendClaude, owner.ID, project.ID)
	calls := 0
	runner := &renameTestRunner{rename: func(agent.AgentRequest, string) error { calls++; return errors.New("provider unavailable") }}
	app := NewApp(store, runner, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	endpoint := "/api/chats/" + chat.ID + "/rename"
	for _, title := range []string{"", " \n\t ", strings.Repeat("a", 201), "bad\x00title"} {
		body, _ := json.Marshal(map[string]string{"title": title})
		if response := apiRequest(app, "POST", endpoint, string(body), cookie, csrf); response.Code != 400 {
			t.Fatalf("invalid title accepted: %q %d", title, response.Code)
		}
	}
	viewerCookie, viewerCSRF := loginRequest(t, app, viewer.Username)
	if response := apiRequest(app, "POST", endpoint, `{"title":"Forbidden"}`, viewerCookie, viewerCSRF); response.Code != 404 || calls != 0 {
		t.Fatalf("read-only user renamed chat: %d, calls %d", response.Code, calls)
	}
	if response := apiRequest(app, "POST", endpoint, `{"title":"Missing CSRF"}`, cookie, ""); response.Code != 403 || calls != 0 {
		t.Fatalf("CSRF check failed: %d", response.Code)
	}
	if response := apiRequest(app, "POST", endpoint, `{"title":"Provider failure"}`, cookie, csrf); response.Code != 502 || calls != 1 {
		t.Fatalf("provider failure hidden: %d %s", response.Code, response.Body.String())
	}
	latest, _ := store.Get(chat.ID)
	if latest.Title != chat.Title || latest.UpdatedAt != chat.UpdatedAt {
		t.Fatal("failed rename changed stored metadata")
	}
}

func TestRenameChatRefreshesCachedCodexName(t *testing.T) {
	var windows codexWindows
	old := &codexWindow{thread: agent.CodexThread{ID: "native-thread", Name: "Old title"}}
	windows.threads = map[string]*codexWindow{"native-thread": old}
	windows.rename("native-thread", "New title")
	if old.thread.Name != "Old title" || windows.threads["native-thread"].thread.Name != "New title" {
		t.Fatal("cached title was not replaced without mutating readers' snapshots")
	}
}
