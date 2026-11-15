package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestCodexFinishTurnUsesTheRolloutIdentityOfAnAnnouncedLegacyTurn(t *testing.T) {
	store := testStore(t)
	chat, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// A rollout whose turns carry no ID of their own.
	exchange := func(prompt, answer string) {
		t.Helper()
		appendTitleRecord(t, path, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}})
		appendTitleRecord(t, path, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": answer}}}})
	}
	exchange("first", "one")
	if err := store.SetNativeSessionPath(chat.ID, "legacy-thread", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = &fakeCodexHistory{legacy: true}
	turn, err := store.beginTurn(chat.ID, "second", chat.Backend)
	if err != nil {
		t.Fatal(err)
	}
	exchange("second", "two")
	// Codex announced an ID for the turn while it ran.
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{TurnID: "announced-live-id"}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) != 2 || latest.Turns[1].ID != turn.ID || latest.Turns[1].Reply != "two" {
		t.Fatalf("a turn announced under an ID its rollout does not record is not one turn: %+v", latest.Turns)
	}
}

func TestCodexFinishTurnDoesNotGuessATurnWithoutAKnownOffset(t *testing.T) {
	for _, tc := range []struct{ name, announced, want string }{
		{"announced and recorded", "native-turn-002", "native-turn-002"},
		{"announced under another ID", "live-only-id", "live-only-id"},
		{"unannounced", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			t.Setenv("CODEX_HOME", home)
			store := testStore(t)
			owner := testUser(t, store, "resumed-owner")
			project := testProject(t, store, owner)
			chat, err := store.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
			if err != nil {
				t.Fatal(err)
			}
			// The session is resumed by ID; its rollout has not been located.
			if err := store.setNativeSession(chat.ID, "resumed-thread", "", false); err != nil {
				t.Fatal(err)
			}
			turn, err := store.beginTurnBy(chat.ID, "third request", chat.Backend, "", nil, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(home, "sessions", "2026", "11", "15")
			if err := os.MkdirAll(folder, 0700); err != nil {
				t.Fatal(err)
			}
			appendNativeTurns(t, filepath.Join(folder, "rollout-resumed-thread.jsonl"), 0, 3, 0)
			if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{TurnID: tc.announced}, nil, chatmodel.StatusCompleted); err != nil {
				t.Fatal(err)
			}
			var identity string
			if err := store.db.QueryRow(`SELECT native_turn_id FROM turn_actors WHERE session_id=? AND turn_id=?`, chat.ID, turn.ID).Scan(&identity); err != nil {
				t.Fatal(err)
			}
			// Where the request began is unknown, so the rollout's first turn is
			// not evidence of which one it is.
			if identity != tc.want {
				t.Fatalf("request bound to %q, want %q", identity, tc.want)
			}
		})
	}
}
