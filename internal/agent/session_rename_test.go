package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexRenameUsesNativeMetadataWithoutStartingTurn(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.NativeSessionID = "native-thread"
	runner := newTestRunner(t)
	if err := runner.RenameSession(t.Context(), request, "New title"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range fixtureLog(t, request) {
		if entry["method"] != "thread/name/set" {
			continue
		}
		value := entry["value"].(map[string]any)
		params := value["params"].(map[string]any)
		if params["threadId"] != request.NativeSessionID || params["name"] != "New title" {
			t.Fatalf("incorrect native rename: %+v", params)
		}
	}
	if fixtureCount(t, request, "thread/name/set") != 1 {
		t.Fatal("native rename was not called")
	}
	for _, method := range []string{"thread/start", "thread/resume", "turn/start"} {
		if fixtureCount(t, request, method) != 0 {
			t.Fatalf("rename invoked %s", method)
		}
	}
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "unsupported-rename"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runner.RenameSession(t.Context(), request, "Unsupported"); err == nil {
		t.Fatal("provider failure was hidden")
	}
}

func TestClaudeRenamePreservesHistoryAndIsReadBySessionList(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	converted, err := writeClaudeCarry(home, directory, []carryItem{{Kind: "user", Text: "Original prompt"}, {Kind: "assistant", Text: "Original response"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(converted.Path)
	request := AgentRequest{Backend: chatmodel.BackendClaude, NativeSessionID: converted.ID, NativeSessionPath: converted.Path, WorkingDirectory: directory}
	runner := newTestRunner(t)
	for _, title := range []string{"First custom title", "重命名对话"} {
		if err := runner.RenameSession(t.Context(), request, title); err != nil {
			t.Fatal(err)
		}
		list, err := (ClaudeSessionLister{}).ListSessions(t.Context(), ListSessionsOptions{Directory: directory})
		if err != nil || len(list) != 1 || list[0].Title != title || list[0].ID != converted.ID {
			t.Fatalf("native discovery did not see rename: %+v %v", list, err)
		}
	}
	after, _ := os.ReadFile(converted.Path)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("rename modified existing transcript")
	}
	index, err := IndexSessionTo(chatmodel.BackendClaude, converted.Path, 0, 0)
	if err != nil || len(index.Turns) != 1 || index.Title != "重命名对话" {
		t.Fatalf("rename changed conversation turns: %+v %v", index, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.RenameSession(ctx, request, "Cancelled"); err == nil {
		t.Fatal("cancelled rename succeeded")
	}
	request.NativeSessionPath += ".missing"
	if err := runner.RenameSession(t.Context(), request, "Missing"); err == nil {
		t.Fatal("missing transcript was created")
	}
}

func TestCodexRenameNativeSmoke(t *testing.T) {
	binary := os.Getenv("SPOOL_CODEX_LIST_SMOKE")
	if binary == "" {
		t.Skip("optional installed app-server smoke is not configured")
	}
	home, directory := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	converted, err := writeCodexCarry(home, directory, "openai", []carryItem{{Kind: "user", Text: "Original prompt"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(converted.Path)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runner := newTestRunner(t)
	if err := runner.RenameSession(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, NativeSessionID: converted.ID}, "Renamed native session"); err != nil {
		t.Fatal(err)
	}
	list, err := (CodexSessionLister{Binary: binary}).ListSessions(ctx, ListSessionsOptions{})
	if err != nil || len(list) != 1 || list[0].ID != converted.ID || list[0].Title != "Renamed native session" {
		t.Fatalf("native rename not visible in thread/list: %+v %v", list, err)
	}
	after, _ := os.ReadFile(converted.Path)
	if string(after) != string(before) {
		t.Fatal("metadata rename altered the Codex conversation")
	}
}
