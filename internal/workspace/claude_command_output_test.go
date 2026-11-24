package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestClaudeLocalCommandHistoryEndpointKeepsCompleteReviewAfterReload(t *testing.T) {
	localSessionHomes(t)
	store := testStore(t)
	owner := testUser(t, store, "review-owner")
	project := testProject(t, store, owner)
	chat, err := store.CreateChat(owner.ID, project.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "review.jsonl")
	data, err := os.ReadFile("../agent/testdata/claude-review.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNativeSessionPath(chat.ID, "review-session", path); err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	for range 2 {
		response := apiRequest(app, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
		var page chatmodel.Session
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Turns) != 1 {
			t.Fatalf("review unavailable after reopening: %d %s", response.Code, response.Body.String())
		}
		turn := page.Turns[0]
		if turn.Status != chatmodel.StatusCompleted || turn.FinishedAt-turn.StartedAt != 1233900 {
			t.Fatalf("review lost its report or completion: %+v", turn)
		}
		if len(page.Transcript.Parts) != 2 || page.Transcript.Parts[0].Text != "/code-review" || page.Transcript.Parts[1].Phase != "final_answer" {
			t.Fatalf("review contains wrappers, missing text or duplicates: %+v", page.Transcript)
		}
		reply := page.Transcript.Parts[1].Text
		if !strings.HasPrefix(reply, "Review complete.\n\n1. **Keep command results visible**") || !strings.HasSuffix(reply, "reopening history must preserve both findings.") || strings.Contains(reply, "local-command-") || strings.Contains(reply, "&lt;") {
			t.Fatalf("review report was hidden or changed: %q", reply)
		}
	}
}
