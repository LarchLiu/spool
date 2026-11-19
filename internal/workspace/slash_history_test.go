package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	chatmodel "spool/internal/model"
)

func slashHistoryPrompts(backend chatmodel.Backend) []string {
	if backend == chatmodel.BackendClaude {
		return []string{"/goal keep testing", "/plan inspect tests", "/code-review main", "/review main", "/loop 5m inspect tests", "/deep-research inspect history", "/gh latest release", "/repo:check", "Continue checking history"}
	}
	return []string{"/goal keep testing", "/plan inspect tests", "/review main", "latest release", "$repo:check", "/skill:inspect:repo verify", "Continue checking history"}
}

func slashHistoryChat(t *testing.T, store *Store, owner, project string, backend chatmodel.Backend) chatmodel.Session {
	t.Helper()
	localSessionHomes(t)
	if backend == chatmodel.BackendCodex {
		// Exercise the rollout projection without invoking a developer's CLI.
		// The app-server projection is covered by the agent protocol tests.
		store.codexHistory.reader = &fakeCodexHistory{legacy: true}
		store.codexHistory.binary, _ = store.codexBinary()
	}
	chat, err := store.CreateChat(owner, project, backend)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "agent", "testdata", string(backend)+"-slash-history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNativeSessionPath(chat.ID, "slash-history", path); err != nil {
		t.Fatal(err)
	}
	return chat
}

func TestSlashCommandHistorySnapshotsKeepTheFirstTurnBeforeContinuation(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			owner := testUser(t, store, "alice")
			project := testProject(t, store, owner)
			chat := slashHistoryChat(t, store, owner.ID, project.ID, backend)
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { _ = app.Close() })
			cookie, _ := loginRequest(t, app, owner.Username)
			response := apiRequest(app, "GET", "/api/chats/"+chat.ID, "", cookie, "")
			var snapshot chatmodel.Session
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
				t.Fatalf("history response: %d %s", response.Code, response.Body.String())
			}
			prompts := slashHistoryPrompts(backend)
			want := len(prompts)
			if len(snapshot.Turns) != want || len(snapshot.Transcript.Parts) != want*2 || snapshot.History.HasMore {
				t.Fatalf("history snapshot lost commands: %d turns, %d parts, %+v", len(snapshot.Turns), len(snapshot.Transcript.Parts), snapshot.History)
			}
			for i, turn := range snapshot.Turns {
				parts := snapshot.Transcript.Parts[turn.PartStart:turn.PartEnd]
				if len(parts) != 2 || turn.InputEnd != turn.PartStart+1 || parts[0].Text != prompts[i] || parts[1].Text != "Reply to "+prompts[i] {
					t.Fatalf("command %d lost its prompt or answer: %+v", i, parts)
				}
			}
		})
	}
}
