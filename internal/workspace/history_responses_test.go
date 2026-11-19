package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func codexReviewTurns() []agent.CodexHistoryTurn {
	return []agent.CodexHistoryTurn{
		{ID: "review-worker", RootID: "review-root", Status: chatmodel.StatusStopped, StartedAt: 1000, Parts: []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "Review the current code changes."},
			{ID: "review-read", Role: "assistant", Kind: "tool_use", Name: "shell", Text: "git diff"},
			{ID: "review-read", Role: "tool", Kind: "tool_result", Text: "diff output"},
		}},
		{ID: "review-root", RootID: "review-root", Status: chatmodel.StatusCompleted, StartedAt: 1000, FinishedAt: 584000, Parts: []chatmodel.Part{
			{ID: "review-answer", Role: "assistant", Kind: "text", Text: "Review complete."},
		}},
	}
}

func TestCodexHistoryGroupsInternalResponseWithoutLosingNativeOwners(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data, err := os.ReadFile("../agent/testdata/codex-review.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, reversed := range []bool{false, true} {
		turns := codexReviewTurns()
		if reversed {
			slices.Reverse(turns)
		}
		chat := store.codexHistorySession(chatmodel.Session{ID: "chat", Backend: "codex", NativeSessionPath: path, Model: "unrelated-current-model"}, turns, "", true)
		if len(chat.Turns) != 1 {
			t.Fatalf("one review rendered as %d responses", len(chat.Turns))
		}
		turn := chat.Turns[0]
		if turn.NativeTurnID != "review-root" || turn.Model != "session-model" || turn.ReasoningEffort != "xhigh" || turn.Status != chatmodel.StatusCompleted || turn.StartedAt != 1000 || turn.FinishedAt != 584000 || turn.Prompt != "Review the current code changes." || turn.Reply != "Review complete." {
			t.Fatalf("merged review: %+v", turn)
		}
		if len(turn.Parts) != 4 || turn.Parts[1].NativeTurnID != "review-worker" || turn.Parts[3].NativeTurnID != "review-root" {
			t.Fatalf("native item owners lost: %+v", turn.Parts)
		}
		ref := toolPartRef(chat.ID, turn, turn.PartStart+1, turn.Parts[1])
		encoded := strings.TrimPrefix(ref, "native-tool-")
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		var locator codexToolLocator
		if err != nil || json.Unmarshal(decoded, &locator) != nil || locator.Turn != "review-worker" || locator.Item != "review-read" {
			t.Fatalf("merged tool points at wrong turn: %s", decoded)
		}
		// Runtime state and actor bindings may still use the announced worker ID.
		store.setRuntimeTurn(chat.ID, chatmodel.Turn{ID: "live-review", NativeTurnID: "review-worker", Status: chatmodel.StatusCompleted})
		if merged := store.withRuntimeTurn(chat); len(merged.Turns) != 1 || merged.Turns[0].ID != "live-review" || merged.Turns[0].Model != "session-model" {
			t.Fatalf("runtime duplicated the root response: %+v", merged.Turns)
		}
		if turns[0].Parts[0].NativeTurnID != "" {
			t.Fatal("projection changed shared native window")
		}
	}
}

type responseHistory struct {
	fakeCodexHistory
	turns []agent.CodexHistoryTurn // oldest first
}

func (f *responseHistory) Item(_ context.Context, _ string, turn, item string) ([]chatmodel.Part, error) {
	f.items++
	var parts []chatmodel.Part
	for _, native := range f.turns {
		if native.ID == turn {
			for _, part := range native.Parts {
				if part.ID == item {
					parts = append(parts, part)
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("no native item %s in %s", item, turn)
	}
	return parts, nil
}

func (f *responseHistory) Turns(_ context.Context, _ string, before string, limit int) (agent.CodexTurnPage, error) {
	end := len(f.turns)
	if before != "" {
		end, _ = strconv.Atoi(before)
	}
	start := max(0, end-limit)
	page := agent.CodexTurnPage{Turns: f.turns[start:end]}
	if start > 0 {
		page.Before = strconv.Itoa(start)
	}
	return page, nil
}

func TestCodexHistoryDoesNotSplitRootResponseAcrossNativePages(t *testing.T) {
	store := testStore(t)
	turns := []agent.CodexHistoryTurn{{ID: "older", RootID: "older", Status: chatmodel.StatusCompleted}}
	turns = append(turns, codexReviewTurns()...)
	for range codexWindowTurnPage - 1 {
		turns = append(turns, agent.CodexHistoryTurn{ID: chatmodel.NewID(), RootID: "unrelated", Parts: []chatmodel.Part{{Role: "assistant", Kind: "text", Text: strings.Repeat("x", 25000)}}})
	}
	reader := &responseHistory{turns: turns}
	chat, path := nativeCodexChat(t, store, reader)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	window, err := readCodexWindow(reader, "thread", info)
	if err != nil || window.before != "1" || len(window.turns) != codexWindowTurnPage+1 || window.turns[0].ID != "review-worker" {
		t.Fatalf("window split a response: %+v %v", window, err)
	}
	page, err := store.codexHistoryPage(context.Background(), chat, nativeHistoryCursor(chat.ID, strconv.Itoa(len(turns))))
	if err != nil || page.History.Before != nativeHistoryCursor(chat.ID, "1") {
		t.Fatalf("older page split a response: %+v %v", page.History, err)
	}
	count := 0
	for _, turn := range page.Turns {
		if turn.NativeTurnID == "review-root" {
			count++
			if turn.Reply != "Review complete." || len(turn.Parts) != 4 {
				t.Fatalf("paged review lost process or answer: %+v", turn)
			}
		}
	}
	if count != 1 {
		t.Fatalf("review response count: %d", count)
	}
}

func TestCodexResponseKeepsRootSettingsAndOpensWithItsInput(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"type":"turn_context","payload":{"turn_id":"worker","root_turn_id":"root","model":"worker-model","effort":"low"}}`,
		`{"type":"turn_context","payload":{"turn_id":"root","effort":"high"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	turns := []agent.CodexHistoryTurn{
		{ID: "worker", RootID: "root", Parts: []chatmodel.Part{{ID: "read", Role: "assistant", Kind: "tool_use", Name: "shell", Text: "git diff"}}},
		{ID: "root", RootID: "root", Status: chatmodel.StatusCompleted, Parts: []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "Review my changes"},
			{ID: "answer", Role: "assistant", Kind: "text", Text: "Done."}}},
	}
	chat := store.codexHistorySession(chatmodel.Session{ID: "chat", Backend: "codex", NativeSessionPath: path}, turns, "", true)
	if len(chat.Turns) != 1 {
		t.Fatalf("response rendered as %d turns", len(chat.Turns))
	}
	turn := chat.Turns[0]
	if turn.Model != "worker-model" || turn.ReasoningEffort != "high" {
		t.Fatalf("internal turn replaced the root's settings: model=%q effort=%q", turn.Model, turn.ReasoningEffort)
	}
	if turn.Prompt != "Review my changes" || turn.InputEnd-turn.PartStart != 1 || len(turn.Parts) != 3 || turn.Parts[0].NativeTurnID != "root" || turn.Parts[1].NativeTurnID != "worker" || turn.Parts[2].Text != "Done." {
		t.Fatalf("user input does not open the response: input=%d parts=%+v", turn.InputEnd-turn.PartStart, turn.Parts)
	}
}

func TestCodexReviewActorMayBeBoundToInternalTurn(t *testing.T) {
	store := testStore(t)
	owner, reviewer := testUser(t, store, "owner"), testUser(t, store, "reviewer")
	project := testProject(t, store, owner, reviewer)
	chat, err := store.CreateChat(owner.ID, project.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.beginTurnBy(chat.ID, "/review", "codex", "", nil, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE turn_actors SET native_turn_id='review-worker' WHERE session_id=? AND turn_id=?`, chat.ID, turn.ID); err != nil {
		t.Fatal(err)
	}
	chat = store.codexHistorySession(chat, codexReviewTurns(), "", false)
	if err := store.attributeTurns(&chat); err != nil || chat.Turns[0].UserID != reviewer.ID {
		t.Fatalf("review actor lost: %+v %v", chat.Turns, err)
	}
}
