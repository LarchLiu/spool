package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexDeleteUsesNativeRPCWithoutStartingTurn(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.NativeSessionID = "native-thread"
	runner := newTestRunner(t)
	if err := runner.DeleteSession(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if fixtureCount(t, request, "thread/delete") != 1 {
		t.Fatal("native deletion was not called exactly once")
	}
	for _, entry := range fixtureLog(t, request) {
		if entry["method"] == "thread/delete" {
			params := entry["value"].(map[string]any)["params"].(map[string]any)
			if params["threadId"] != request.NativeSessionID {
				t.Fatalf("wrong deletion target: %+v", params)
			}
		}
	}
	for _, method := range []string{"thread/start", "thread/resume", "turn/start"} {
		if fixtureCount(t, request, method) != 0 {
			t.Fatalf("delete invoked %s", method)
		}
	}
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "unsupported-delete"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runner.DeleteSession(t.Context(), request); err == nil {
		t.Fatal("native failure was hidden")
	}
}

func TestClaudeDeleteRemovesNativeHistoryAndSubagents(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := writeClaudeListFixture(t, home, project, "delete", 1, claudeListUser(project, "Delete this history"))
	other := writeClaudeListFixture(t, home, project, "keep", 2, claudeListUser(project, "Keep this history"))
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	directory := strings.TrimSuffix(path, ".jsonl")
	if err := os.MkdirAll(filepath.Join(directory, "subagents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "subagents", "agent.jsonl"), []byte("subagent history"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := archiveClaudeSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	lister := NewClaudeSessionLister()
	_, _ = lister.ListSessions(t.Context(), ListSessionsOptions{})
	request := AgentRequest{Backend: chatmodel.BackendClaude, NativeSessionID: id, Binary: "does-not-exist"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (&CLIRunner{}).DeleteSession(ctx, request); err == nil {
		t.Fatal("cancelled deletion succeeded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancelled deletion removed history", err)
	}
	if err := (&CLIRunner{}).DeleteSession(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{path, directory} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("native history still exists: %s %v", removed, err)
		}
	}
	if body, err := os.ReadFile(other); err != nil || len(body) == 0 {
		t.Fatal("deletion affected another session", err)
	}
	list, err := lister.ListSessions(t.Context(), ListSessionsOptions{})
	if err != nil || len(list) != 1 || list[0].Path != other {
		t.Fatalf("cached list retained deleted session: %+v %v", list, err)
	}
	if err := (&CLIRunner{}).DeleteSession(t.Context(), request); !os.IsNotExist(err) {
		t.Fatal("missing session did not fail", err)
	}
	request.NativeSessionID = "../keep"
	if err := (&CLIRunner{}).DeleteSession(t.Context(), request); err == nil {
		t.Fatal("invalid session ID accepted")
	}
}

func TestClaudeDeleteSDKParity(t *testing.T) {
	sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS")
	if sdk == "" {
		t.Skip("optional SDK parity oracle is not configured")
	}
	for _, action := range []string{"go", "sdk"} {
		t.Run(action, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", home)
			path := writeClaudeListFixture(t, home, project, "delete-sdk", 1, claudeListUser(project, "Delete this history"))
			id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			if err := archiveClaudeSession(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if action == "go" {
				if err := deleteClaudeSession(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			} else if body, err := exec.Command("bun", "../../tools/claude-session-delete-oracle.mjs", sdk, id).CombinedOutput(); err != nil {
				t.Fatalf("SDK delete: %v %s", err, body)
			}
			rows := claudeArchiveOracle(t, sdk, "list", id)
			if len(rows) != 0 {
				t.Fatalf("SDK still lists deleted history: %+v", rows)
			}
			for _, removed := range []string{path, strings.TrimSuffix(path, ".jsonl")} {
				if _, err := os.Stat(removed); !os.IsNotExist(err) {
					t.Fatal("deletion did not remove native files", removed, err)
				}
			}
		})
	}
}

func TestCodexDeleteNativeSmoke(t *testing.T) {
	binary := os.Getenv("SPOOL_CODEX_LIST_SMOKE")
	if binary == "" {
		t.Skip("optional installed app-server smoke is not configured")
	}
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	converted, err := writeCodexCarry(home, project, []carryItem{{Kind: "user", Text: "Disposable deletion fixture"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := (&CLIRunner{}).DeleteSession(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, NativeSessionID: converted.ID}); err != nil {
		t.Fatal(err)
	}
	list, err := (CodexSessionLister{Binary: binary}).ListSessions(ctx, ListSessionsOptions{})
	if err != nil || len(list) != 0 {
		body, _ := json.Marshal(list)
		t.Fatalf("deleted thread is still listed: %s %v", body, err)
	}
	if _, err := os.Stat(converted.Path); !os.IsNotExist(err) {
		t.Fatal("native rollout was retained", err)
	}
}
