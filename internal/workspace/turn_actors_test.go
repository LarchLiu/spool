package workspace

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func participantIDs(chat chatmodel.Session) []string {
	var ids []string
	for _, person := range chat.Participants {
		ids = append(ids, person.ID)
	}
	return ids
}

func TestTurnActorsSurviveRestartWithLegacyOwnerFallback(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
			store := testStore(t)
			alice, bob := testUser(t, store, "alice"), testUser(t, store, "bob")
			project := testProject(t, store, alice, bob)
			chat, _ := store.CreateChat(alice.ID, project.ID, backend)
			app := NewApp(store, &historyRunner{}, "", "")
			t.Cleanup(func() { _ = app.Close() })
			if _, err := app.runTurn(context.Background(), chat.ID, "legacy message", nil); err != nil {
				t.Fatal(err)
			}
			// Simulate a pre-feature native turn without any author metadata.
			if _, err := store.db.Exec(`DELETE FROM turn_actors WHERE session_id=?`, chat.ID); err != nil {
				t.Fatal(err)
			}
			chat, _ = store.Get(chat.ID)
			done := make(chan error, 1)
			if err := app.startTurnWithCallback(chat, messageInput{Message: "Bob's message", userID: bob.ID}, chatmodel.CodexOptions{}, func(err error) { done <- err }); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := store.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "bob-picked"}); err != nil {
				t.Fatal(err)
			}
			if err := app.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			loaded, ok := reopened.Get(chat.ID)
			if !ok || len(loaded.Turns) != 2 || loaded.Turns[0].UserID != alice.ID || loaded.Turns[1].UserID != bob.ID {
				t.Fatalf("authors did not survive native hydration/restart: %+v", loaded.Turns)
			}
			if loaded.Turns[0].Prompt != "legacy message" || loaded.Turns[1].Prompt != "Bob's message" {
				t.Fatal("actor metadata changed the native transcript")
			}
			if len(loaded.Participants) != 2 {
				t.Fatalf("legacy owner or actual operator missing: %+v", loaded.Participants)
			}
			for _, person := range loaded.Participants {
				if person.ID == bob.ID && (person.Avatar.Style != "thumbs" || person.Avatar.Seed != "bob-picked") {
					t.Fatal("operator avatar was not loaded")
				}
			}
			navigation, err := reopened.UserNavigation(alice.ID)
			if err != nil || len(navigation.Chats) != 1 || !reflect.DeepEqual(navigation.Chats[0].Participants, loaded.Participants) || len(navigation.Chats[0].Turns) != 0 {
				t.Fatalf("navigation must return operator metadata without history: %+v, %v", navigation, err)
			}
			assertNoTurnTables(t, reopened)
		})
	}
}

func TestTurnActorsRecoverUnannouncedNativeTurnAndAuditActions(t *testing.T) {
	store := testStore(t)
	alice, bob := testUser(t, store, "alice"), testUser(t, store, "bob")
	project := testProject(t, store, alice, bob)
	chat, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	bot, err := store.beginTurnBy(chat.ID, "automated", chat.Backend, "", nil, chatmodel.SpoolBotID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	if err = store.SetNativeSessionPath(chat.ID, "actor-native", path); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTurn(chat.ID, bot.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Get(chat.ID)
	if !reflect.DeepEqual(participantIDs(loaded), []string{chatmodel.SpoolBotID}) || !loaded.Participants[0].Bot || loaded.Turns[0].UserID != chatmodel.SpoolBotID {
		t.Fatalf("automatic turns must be bot-owned, not owner-operated: %+v", loaded)
	}
	human, err := store.beginTurnBy(chat.ID, "follow-up", chat.Backend, "", nil, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.recordTurnAction(chat.ID, human.ID, alice.ID, "deny", "approval-id"); err != nil {
		t.Fatal(err)
	}
	appendNativeTurns(t, path, 1, 1, 0)
	// No native turn event or FinishTurn: the persisted offset must recover Bob
	// after a process crash, without assigning him the preceding bot turn.
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, ok := reopened.Get(chat.ID)
	if !ok || len(loaded.Turns) != 2 || loaded.Turns[0].UserID != chatmodel.SpoolBotID || loaded.Turns[1].UserID != bob.ID {
		t.Fatalf("unannounced turn attribution lost: %+v", loaded.Turns)
	}
	if len(loaded.Turns[1].Actions) != 1 || loaded.Turns[1].Actions[0].UserID != alice.ID || loaded.Turns[1].Actions[0].Kind != "deny" || len(loaded.Participants) != 3 {
		t.Fatalf("audit operator missing after restart: %+v", loaded)
	}
	// A different chat using the same native file must not inherit identities.
	other, _ := reopened.CreateChat(alice.ID, project.ID, chat.Backend)
	if err = reopened.SetNativeSessionPath(other.ID, "actor-native", path); err != nil {
		t.Fatal(err)
	}
	separate, _ := reopened.Get(other.ID)
	if separate.Turns[0].UserID != alice.ID || separate.Turns[1].UserID != alice.ID || len(separate.Turns[1].Actions) != 0 {
		t.Fatal("shared native cache leaked another chat's actors")
	}
}

func TestTurnActorRejectsClientSpoofing(t *testing.T) {
	store := testStore(t)
	alice := testUser(t, store, "alice")
	project := testProject(t, store, alice)
	testAgent(t, store)
	chat, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, csrf := loginRequest(t, app, "alice")
	for _, field := range []string{"user_id", "userID", "actor_kind"} {
		w := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"spoof","`+field+`":"spool-bot"}`, cookie, csrf)
		if w.Code != 400 {
			t.Fatalf("accepted client-supplied actor %s: %d", field, w.Code)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM turn_actors`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected send persisted an actor: %d %v", count, err)
	}
}

func TestTurnActorSchemaUpgradePreservesExistingMetadata(t *testing.T) {
	store := testStore(t)
	alice, bob := testUser(t, store, "alice"), testUser(t, store, "bob")
	project := testProject(t, store, alice, bob)
	chat, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	turn, err := store.BeginTurn(chat.ID, "existing actor")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	if err = store.SetNativeSessionPath(chat.ID, "existing-actor-native", path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.recordTurnAction(chat.ID, turn.ID, bob.ID, "approve", "existing-approval"); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	// Reproduce a database created by the earlier actor table definition.
	if _, err = store.db.Exec(`ALTER TABLE turn_actors DROP COLUMN actor_kind`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		reopened, err := OpenStore(store.Root())
		if err != nil {
			t.Fatal(err)
		}
		var kind string
		if err = reopened.db.QueryRow(`SELECT actor_kind FROM turn_actors WHERE session_id=? AND turn_id=?`, chat.ID, turn.ID).Scan(&kind); err != nil || kind != "user" {
			t.Fatalf("old actor row was not migrated: kind=%q err=%v", kind, err)
		}
		loaded, ok := reopened.Get(chat.ID)
		if !ok || len(loaded.Turns) != 1 || loaded.Turns[0].UserID != alice.ID || len(loaded.Turns[0].Actions) != 1 || loaded.Turns[0].Actions[0].UserID != bob.ID {
			t.Fatalf("migration lost turn bindings or audit data: %+v", loaded)
		}
		navigation, err := reopened.UserNavigation(alice.ID)
		if err != nil || len(navigation.Chats) != 1 || len(navigation.Chats[0].Participants) != 2 {
			t.Fatalf("navigation failed after upgrading the old actor schema: %+v, %v", navigation, err)
		}
		if err = reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
