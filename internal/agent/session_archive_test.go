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

func TestCodexArchiveUsesNativeRPCWithoutStartingTurn(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.NativeSessionID = "native-thread"
	runner := newTestRunner(t)
	if err := runner.ArchiveSession(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if fixtureCount(t, request, "thread/archive") != 1 {
		t.Fatal("native archive was not called exactly once")
	}
	for _, entry := range fixtureLog(t, request) {
		if entry["method"] == "thread/archive" {
			params := entry["value"].(map[string]any)["params"].(map[string]any)
			if params["threadId"] != request.NativeSessionID {
				t.Fatalf("wrong native session: %+v", params)
			}
		}
	}
	for _, method := range []string{"thread/start", "thread/resume", "turn/start"} {
		if fixtureCount(t, request, method) != 0 {
			t.Fatalf("archive invoked %s", method)
		}
	}
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "unsupported-archive"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runner.ArchiveSession(t.Context(), request); err == nil {
		t.Fatal("native failure was hidden")
	}
}

func TestClaudeArchivePreservesHistoryAndRefreshesCachedList(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := writeClaudeListFixture(t, home, project, "archive", 1, claudeListUser(project, "Keep this history"))
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	before, _ := os.ReadFile(path)
	duplicate := filepath.Join(home, "projects", "another-project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(duplicate), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicate, before, 0600); err != nil {
		t.Fatal(err)
	}
	lister := NewClaudeSessionLister()
	list, err := lister.ListSessions(t.Context(), ListSessionsOptions{})
	if err != nil || len(list) != 1 || list[0].Archived {
		t.Fatalf("initial discovery: %+v %v", list, err)
	}
	request := AgentRequest{Backend: chatmodel.BackendClaude, NativeSessionID: id, Binary: "does-not-exist"}
	if err := (&CLIRunner{}).ArchiveSession(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	for _, transcript := range []string{path, duplicate} {
		after, _ := os.ReadFile(transcript)
		if string(after) != string(before) {
			t.Fatal("archive changed the native transcript")
		}
		body, err := os.ReadFile(claudeArchivePath(transcript, id))
		var mark struct {
			Archived bool
			At, By   string
		}
		if err != nil || json.Unmarshal(body, &mark) != nil || !mark.Archived || mark.By != "sdk" {
			t.Fatalf("invalid SDK archive mark: %s %v", body, err)
		}
		if _, err := time.Parse(time.RFC3339Nano, mark.At); err != nil {
			t.Fatal(err)
		}
	}
	list, err = lister.ListSessions(t.Context(), ListSessionsOptions{})
	if err != nil || len(list) != 1 || !list[0].Archived {
		t.Fatalf("cached discovery missed the archive: %+v %v", list, err)
	}
	var index LocalSessionIndex
	visible, err := index.Discover(t.Context(), map[chatmodel.Backend]SessionLister{chatmodel.BackendClaude: lister})
	if err != nil || len(visible) != 0 {
		t.Fatalf("archived chat remained visible: %+v %v", visible, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (&CLIRunner{}).ArchiveSession(ctx, request); err == nil {
		t.Fatal("cancelled archive succeeded")
	}
	request.NativeSessionID = "00000000-0000-0000-0000-000000000000"
	if err := (&CLIRunner{}).ArchiveSession(t.Context(), request); err == nil {
		t.Fatal("missing session was archived")
	}
}

func TestClaudeArchiveHumanPromptAndSDKParity(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	for _, kind := range []string{"user", "human-origin", "sdk-origin", "assistant", "tool-result", "queued-command", "image", "meta", "sidechain", "virtual"} {
		t.Run(kind, func(t *testing.T) {
			path := writeClaudeListFixture(t, home, project, "archive-"+kind, 1, claudeListUser(project, "Original prompt"))
			id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			if err := archiveClaudeSession(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			lister := NewClaudeSessionLister()
			check := func(want bool) {
				t.Helper()
				list, err := lister.ListSessions(t.Context(), ListSessionsOptions{})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, session := range list {
					if session.ID == id {
						found = true
						if session.Archived != want {
							t.Fatalf("archived=%v, want %v", session.Archived, want)
						}
					}
				}
				if !found {
					t.Fatal("missing session")
				}
				if sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS"); sdk != "" {
					rows := claudeArchiveOracle(t, sdk, "list", id)
					if rows[id] != want {
						t.Fatalf("SDK archived=%v, Go archived=%v", rows[id], want)
					}
				}
			}
			check(true)
			record := claudeListUser(project, "Continue conversation")
			record["timestamp"] = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
			want := false
			switch kind {
			case "human-origin":
				record["origin"] = map[string]any{"kind": "human"}
			case "sdk-origin":
				record["origin"] = map[string]any{"kind": "sdk"}
				want = true
			case "assistant":
				record["type"] = "assistant"
				want = true
			case "tool-result":
				record["message"] = map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "tool output"}, map[string]any{"type": "text", "text": "tool text"}}}
				want = true
			case "queued-command":
				record["type"] = "attachment"
				record["attachment"] = map[string]any{"type": "queued_command", "prompt": "Continue conversation", "origin": map[string]any{"kind": "human"}}
			case "image":
				record["origin"] = map[string]any{"kind": "human"}
				record["message"] = map[string]any{"content": []any{map[string]any{"type": "image", "source": map[string]any{"data": "fixture"}}}}
			case "meta":
				record["isMeta"] = true
				want = true
			case "sidechain":
				record["isSidechain"] = true
				want = true
			case "virtual":
				record["isVirtual"] = true
				want = true
			}
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			err = json.NewEncoder(file).Encode(record)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			check(want)
		})
	}
}

func TestClaudeArchiveSDKMutationParity(t *testing.T) {
	sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS")
	if sdk == "" {
		t.Skip("optional SDK parity oracle is not configured")
	}
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := writeClaudeListFixture(t, home, project, "sdk-archive", 1, claudeListUser(project, "Original prompt"))
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	lister := NewClaudeSessionLister()
	_, _ = lister.ListSessions(t.Context(), ListSessionsOptions{})
	for _, action := range []string{"archive", "unarchive", "archive"} {
		expected := claudeArchiveOracle(t, sdk, action, id)
		actual, err := lister.ListSessions(t.Context(), ListSessionsOptions{})
		if err != nil || len(actual) != 1 || actual[0].Archived != expected[id] {
			t.Fatalf("%s: Go=%+v SDK=%+v err=%v", action, actual, expected, err)
		}
	}
}

func claudeArchiveOracle(t *testing.T, sdk, action, id string) map[string]bool {
	t.Helper()
	body, err := exec.Command("bun", "../../tools/claude-session-archive-oracle.mjs", sdk, action, id).CombinedOutput()
	if err != nil {
		t.Fatalf("SDK oracle: %v %s", err, body)
	}
	var rows []struct {
		SessionID string
		Archived  bool
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	result := map[string]bool{}
	for _, row := range rows {
		result[row.SessionID] = row.Archived
	}
	return result
}

func TestCodexArchiveNativeSmoke(t *testing.T) {
	binary := os.Getenv("SPOOL_CODEX_LIST_SMOKE")
	if binary == "" {
		t.Skip("optional installed app-server smoke is not configured")
	}
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	converted, err := writeCodexCarry(home, project, "openai", []carryItem{{Kind: "user", Text: "Preserve native history"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(converted.Path)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := (&CLIRunner{}).ArchiveSession(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, NativeSessionID: converted.ID}); err != nil {
		t.Fatal(err)
	}
	list, err := (CodexSessionLister{Binary: binary}).ListSessions(ctx, ListSessionsOptions{})
	if err != nil || len(list) != 0 {
		t.Fatalf("archived thread is still listed: %+v %v", list, err)
	}
	archived, err := (CodexSessionLister{Binary: binary}).ListArchivedSessions(ctx)
	if err != nil || len(archived) != 1 || archived[0].ID != converted.ID || !archived[0].Archived {
		t.Fatalf("native archive catalogue omitted thread: %+v %v", archived, err)
	}
	found := false
	err = filepath.WalkDir(filepath.Join(home, "archived_sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), converted.ID) {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = strings.Contains(string(body), strings.TrimSpace(string(before)))
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("native archived history was not preserved: %v", err)
	}
}
