package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func conversionHomes(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	return root
}

func TestNativeSessionConversionBothDirections(t *testing.T) {
	root := conversionHomes(t)
	for _, from := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(from), func(t *testing.T) {
			to := chatmodel.BackendClaude
			if from == chatmodel.BackendClaude {
				to = chatmodel.BackendCodex
			}
			source := filepath.Join("testdata", "carry", string(from)+"-tools.jsonl")
			before, _ := os.ReadFile(source)
			converted, err := ConvertSession(from, to, source, root)
			if err != nil {
				t.Fatal(err)
			}
			if !chatmodel.ValidID(converted.ID) || !strings.Contains(converted.ID, "-") {
				t.Fatal("target requires a native UUID", converted.ID)
			}
			data, err := os.ReadFile(converted.Path)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"encrypted_content", "redacted_thinking", `"thinking"`, "base_instructions", `"role":"developer"`} {
				if bytes.Contains(data, []byte(forbidden)) {
					t.Fatal("provider-private context crossed agents:", forbidden)
				}
			}
			if to == chatmodel.BackendClaude && (!bytes.Contains(data, []byte(`"model":"<synthetic>"`)) || !bytes.Contains(data, []byte(`"name":"Bash"`))) {
				t.Fatal("missing synthetic assistant model or translated Bash tool")
			}
			if to == chatmodel.BackendCodex && !bytes.Contains(data, []byte(`"name":"exec_command"`)) {
				t.Fatal("missing translated Codex shell tool")
			}
			parsed, err := ParseSessionTranscript(to, converted.Path)
			if err != nil || len(parsed.Parts) < 4 {
				t.Fatalf("unreadable converted history: %+v %v", parsed, err)
			}
			for _, part := range parsed.Parts {
				if part.Kind == "thinking" {
					t.Fatal("reasoning crossed agents")
				}
			}
			back, err := ConvertSession(to, from, converted.Path, root)
			if err != nil {
				t.Fatal("round trip:", err)
			}
			if back.ID == converted.ID {
				t.Fatal("round trip reused a stale provider session")
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(before, after) {
				t.Fatal("source was modified")
			}
			info, _ := os.Stat(converted.Path)
			if info.Mode().Perm() != 0o600 {
				t.Fatal("converted history is not private")
			}
		})
	}
}

func TestConversionPreservesImagesAndUserXML(t *testing.T) {
	root := conversionHomes(t)
	image := "data:image/png;base64,aGVsbG8="
	source, err := writeCodexCarry(filepath.Join(root, "codex"), root, []carryItem{
		{Kind: "user", Text: "<request>keep this user XML</request>", Images: []string{image}},
		{Kind: "assistant", Text: "Image received"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	converted, err := ConvertSession(chatmodel.BackendCodex, chatmodel.BackendClaude, source.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	items, err := claudeCarryItems(converted.Path)
	if err != nil || len(items) != 2 || len(items[0].Images) != 1 || items[0].Images[0] != image || items[0].Text != "<request>keep this user XML</request>" {
		t.Fatalf("lost multimodal user input: %+v %v", items, err)
	}
}

func TestConversionHonorsBranchesAndCompaction(t *testing.T) {
	root := conversionHomes(t)
	path := filepath.Join(root, "claude.jsonl")
	records := []map[string]any{
		{"uuid": "old", "type": "user", "message": map[string]any{"content": "obsolete"}},
		{"uuid": "boundary", "type": "system"},
		{"uuid": "summary", "parentUuid": "boundary", "type": "user", "message": map[string]any{"content": "compacted summary"}},
		{"uuid": "discarded", "parentUuid": "summary", "type": "assistant", "message": map[string]any{"content": "abandoned branch"}},
		{"uuid": "current", "parentUuid": "summary", "type": "assistant", "message": map[string]any{"content": "active branch"}},
		{"uuid": "side", "parentUuid": "old", "type": "assistant", "isSidechain": true, "message": map[string]any{"content": "sidechain"}},
	}
	var body bytes.Buffer
	for _, record := range records {
		_ = json.NewEncoder(&body).Encode(record)
	}
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	converted, err := ConvertSession(chatmodel.BackendClaude, chatmodel.BackendCodex, path, root)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(converted.Path)
	for _, forbidden := range []string{"obsolete", "abandoned branch", "sidechain"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatal("inactive context was restored:", forbidden)
		}
	}
	if !bytes.Contains(data, []byte("compacted summary")) || !bytes.Contains(data, []byte("active branch")) {
		t.Fatal("lost active context")
	}
	path = filepath.Join(root, "codex.jsonl")
	body.Reset()
	_ = json.NewEncoder(&body).Encode(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "obsolete"}}}})
	_ = json.NewEncoder(&body).Encode(map[string]any{"type": "compacted", "payload": map[string]any{"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "replacement summary"}}}}}})
	_ = os.WriteFile(path, body.Bytes(), 0o600)
	converted, err = ConvertSession(chatmodel.BackendCodex, chatmodel.BackendClaude, path, root)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(converted.Path)
	if bytes.Contains(data, []byte("obsolete")) || !bytes.Contains(data, []byte("replacement summary")) {
		t.Fatal("Codex compaction was ignored")
	}
}

// Opt-in compatibility check: starts installed CLIs with isolated homes and
// restores history via control requests only. It never submits a user prompt.
func TestConvertedNativeSessionResumesWithoutPrompt(t *testing.T) {
	if os.Getenv("SPOOL_TEST_NATIVE_RESUME") != "1" {
		t.Skip("set SPOOL_TEST_NATIVE_RESUME=1 to check installed native CLIs without inference")
	}
	root := conversionHomes(t)
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			name, from := "codex", chatmodel.BackendClaude
			if backend == chatmodel.BackendClaude {
				name, from = "claude", chatmodel.BackendCodex
			}
			binary, err := exec.LookPath(name)
			if err != nil {
				t.Skip(err)
			}
			converted, err := ConvertSession(from, backend, filepath.Join("testdata", "carry", string(from)+"-tools.jsonl"), root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			process, err := startAgentProcess(ctx, AgentRequest{Backend: backend, Binary: binary, WorkingDirectory: root, NativeSessionID: converted.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { cancel(); <-process.done }()
			if backend == chatmodel.BackendCodex {
				if err := process.initializeCodex(ctx); err != nil {
					t.Fatal(err)
				}
				response, err := process.codexRPC(ctx, "thread/resume", map[string]any{"threadId": converted.ID, "path": converted.Path, "cwd": root, "excludeTurns": false})
				if err != nil || nestedString(response, "thread", "id") != converted.ID {
					t.Fatalf("converted session was not resumed: %+v %v", response, err)
				}
				thread, _ := response["thread"].(map[string]any)
				turns, _ := thread["turns"].([]any)
				if len(turns) == 0 {
					t.Fatal("Codex resumed an empty history instead of the converted conversation")
				}
			} else {
				if _, err := process.claudeControl(ctx, map[string]any{"subtype": "initialize"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRunnerSwitchesProcessAndResumesConvertedContext(t *testing.T) {
	root := conversionHomes(t)
	request := newAgentFixture(t, chatmodel.BackendCodex)
	runner := newTestRunner(t)
	_, _ = runFixture(t, runner, request)
	for _, backend := range []chatmodel.Backend{chatmodel.BackendClaude, chatmodel.BackendCodex} {
		from := chatmodel.BackendCodex
		if backend == chatmodel.BackendCodex {
			from = chatmodel.BackendClaude
		}
		converted, err := ConvertSession(from, backend, filepath.Join("testdata", "carry", string(from)+"-tools.jsonl"), root)
		if err != nil {
			t.Fatal(err)
		}
		request.Backend, request.NativeSessionID, request.NativeSessionPath = backend, converted.ID, converted.Path
		_, _ = runFixture(t, runner, request)
		_, _ = runFixture(t, runner, request)
		if backend == chatmodel.BackendClaude {
			found := false
			for _, record := range fixtureLog(t, request) {
				args, _ := record["args"].([]any)
				for i, arg := range args {
					if arg == "--resume" && i+1 < len(args) && args[i+1] == converted.ID {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("Claude did not resume the converted native session")
			}
		} else {
			found := false
			for _, record := range fixtureLog(t, request) {
				value, _ := record["value"].(map[string]any)
				if record["method"] == "thread/resume" && agentfixture.NestedString(value, "params", "path") == converted.Path && agentfixture.NestedString(value, "params", "threadId") == converted.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("Codex did not resume the converted native session by path")
			}
		}
	}
	if fixtureCount(t, request, "spawn") != 3 || fixtureCount(t, request, "turn/start") != 3 || fixtureCount(t, request, "user") != 2 {
		t.Fatal("switch did not replace the process, or same-agent continuation restarted it")
	}
}
