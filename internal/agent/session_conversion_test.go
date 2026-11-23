package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

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
			converted, err := ConvertSession(from, to, source, root, "openai")
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
			if to == chatmodel.BackendCodex {
				if !bytes.Contains(data, []byte(`"name":"exec_command"`)) {
					t.Fatal("missing translated Codex shell tool")
				}
				var meta struct {
					Payload struct {
						ModelProvider string `json:"model_provider"`
					}
				}
				if err := json.Unmarshal(bytes.SplitN(data, []byte("\n"), 2)[0], &meta); err != nil || meta.Payload.ModelProvider != "openai" {
					t.Fatalf("new native session has no provider binding: %+v, %v", meta, err)
				}
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
			back, err := ConvertSession(to, from, converted.Path, root, "openai")
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

func TestCodexConversionRejectsEmptyProviderWithoutWritingSession(t *testing.T) {
	root := conversionHomes(t)
	source := filepath.Join("testdata", "carry", "claude-tools.jsonl")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"", " \t"} {
		if _, err := ConvertSession(chatmodel.BackendClaude, chatmodel.BackendCodex, source, root, provider); err == nil {
			t.Fatal("conversion accepted an empty provider")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "codex", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("failed conversion created native session storage: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed conversion modified the source", err)
	}
}

func TestConversionPreservesImagesAndUserXML(t *testing.T) {
	root := conversionHomes(t)
	image := "data:image/png;base64,aGVsbG8="
	source, err := writeCodexCarry(filepath.Join(root, "codex"), root, "openai", []carryItem{
		{Kind: "user", Text: "<request>keep this user XML</request>", Images: []string{image}},
		{Kind: "assistant", Text: "Image received"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	converted, err := ConvertSession(chatmodel.BackendCodex, chatmodel.BackendClaude, source.Path, root, "")
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
	converted, err := ConvertSession(chatmodel.BackendClaude, chatmodel.BackendCodex, path, root, "openai")
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
	converted, err = ConvertSession(chatmodel.BackendCodex, chatmodel.BackendClaude, path, root, "")
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
	for _, tc := range []struct {
		name                 string
		backend              chatmodel.Backend
		provider             string
		emptyIndexedProvider bool
	}{
		{"codex", chatmodel.BackendCodex, "openai", false},
		{"codex-custom-provider", chatmodel.BackendCodex, "conversion-test", false},
		{"codex-indexed-empty-provider-default", chatmodel.BackendCodex, "openai", true},
		{"codex-indexed-empty-provider", chatmodel.BackendCodex, "conversion-test", true},
		{"claude", chatmodel.BackendClaude, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := conversionHomes(t)
			backend := tc.backend
			name, from := "codex", chatmodel.BackendClaude
			if backend == chatmodel.BackendClaude {
				name, from = "claude", chatmodel.BackendCodex
			}
			binary, err := exec.LookPath(name)
			if err != nil {
				t.Skip(err)
			}
			if tc.provider == "conversion-test" {
				configureConversionCodexProvider(t)
			}
			provider := ""
			if backend == chatmodel.BackendCodex {
				if err := os.MkdirAll(os.Getenv("CODEX_HOME"), 0700); err != nil {
					t.Fatal(err)
				}
				runner := newTestRunner(t)
				runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: binary})
				provider, err = runner.CodexModelProvider(context.Background(), root)
				if err != nil || provider != tc.provider {
					t.Fatalf("wrong effective provider: %q, %v", provider, err)
				}
			}
			converted, err := ConvertSession(from, backend, filepath.Join("testdata", "carry", string(from)+"-tools.jsonl"), root, provider)
			if err != nil {
				t.Fatal(err)
			}
			if backend == chatmodel.BackendCodex {
				claude, err := ConvertSession(backend, from, converted.Path, root, "")
				if err != nil {
					t.Fatal(err)
				}
				converted, err = ConvertSession(from, backend, claude.Path, root, provider)
				if err != nil {
					t.Fatal(err)
				}
			}
			assertConvertedNativeResume(t, backend, binary, root, converted, tc.provider, tc.emptyIndexedProvider)
		})
	}
}

func configureConversionCodexProvider(t *testing.T) {
	t.Helper()
	home := os.Getenv("CODEX_HOME")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := `model_provider = "conversion-test"
model = "conversion-model"
[model_providers.conversion-test]
name = "Conversion test"
base_url = "http://127.0.0.1:1/v1"
wire_api = "responses"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertConvertedNativeResume(t *testing.T, backend chatmodel.Backend, binary, directory string, converted ConvertedSession, provider string, emptyIndexedProvider bool) {
	t.Helper()
	if backend == chatmodel.BackendCodex {
		if _, err := (CodexSessionLister{Binary: binary}).ListSessions(context.Background(), ListSessionsOptions{}); err != nil {
			t.Fatal("converted sessions could not be listed before resume:", err)
		}
		if emptyIndexedProvider {
			paths, err := filepath.Glob(filepath.Join(os.Getenv("CODEX_HOME"), "state_*.sqlite"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("expected one isolated Codex state database: %v, %v", paths, err)
			}
			db, err := sql.Open("sqlite", paths[0])
			if err != nil {
				t.Fatal(err)
			}
			result, err := db.Exec(`UPDATE threads SET model_provider='' WHERE id=?`, converted.ID)
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			updated, err := result.RowsAffected()
			db.Close()
			if err != nil || updated != 1 {
				t.Fatalf("could not reproduce empty indexed provider: %d, %v", updated, err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := startAgentProcess(ctx, AgentRequest{Backend: backend, Binary: binary, WorkingDirectory: directory, NativeSessionID: converted.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); <-process.done }()
	if backend == chatmodel.BackendClaude {
		if _, err := process.claudeControl(ctx, map[string]any{"subtype": "initialize"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := process.initializeCodex(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := process.codexRPC(ctx, "thread/read", map[string]any{"threadId": converted.ID, "includeTurns": false}); err != nil {
		t.Fatal("converted session could not be read before resume:", err)
	}
	params := map[string]any{"threadId": converted.ID, "path": converted.Path, "cwd": directory, "model": "conversion-model", "excludeTurns": false}
	if emptyIndexedProvider {
		if _, err := process.codexRPC(ctx, "thread/resume", params); err == nil || !strings.Contains(err.Error(), "This chat uses model provider “”, which is no longer configured.") {
			t.Fatalf("expected native empty-provider failure before recovery, got %v", err)
		}
	}
	var response map[string]any
	if emptyIndexedProvider {
		response, err = process.resumeCodex(ctx, params)
	} else {
		// New conversions must resume natively, without provider recovery.
		response, err = process.codexRPC(ctx, "thread/resume", params)
	}
	if err != nil || nestedString(response, "thread", "id") != converted.ID {
		t.Fatalf("converted session was not resumed: %+v %v", response, err)
	}
	if response["modelProvider"] != provider {
		t.Fatalf("converted session resumed with provider %q, want %q", response["modelProvider"], provider)
	}
	thread, _ := response["thread"].(map[string]any)
	turns, _ := thread["turns"].([]any)
	if len(turns) == 0 {
		t.Fatal("Codex resumed an empty history instead of the converted conversation")
	}
	indexed, err := IndexSessionTurns(backend, converted.Path, 0)
	if err != nil || len(indexed) != len(turns) {
		t.Fatalf("native and indexed converted turns disagree: %d vs %d, %v", len(turns), len(indexed), err)
	}
	for i, raw := range turns {
		turn, _ := raw.(map[string]any)
		if turn["id"] != indexed[i].ID {
			t.Fatalf("native resume lost converted identity: %v != %s", turn["id"], indexed[i].ID)
		}
	}
}

func TestConvertedNativeSessionRepeatedSwitchesResumeWithoutPrompt(t *testing.T) {
	if os.Getenv("SPOOL_TEST_NATIVE_RESUME") != "1" {
		t.Skip("set SPOOL_TEST_NATIVE_RESUME=1 to check installed native CLIs without inference")
	}
	binaries := map[chatmodel.Backend]string{}
	for _, backend := range []chatmodel.Backend{"claude", "codex"} {
		binary, err := exec.LookPath(string(backend))
		if err != nil {
			t.Skip(err)
		}
		binaries[backend] = binary
	}
	for _, provider := range []string{"openai", "conversion-test"} {
		t.Run(provider, func(t *testing.T) {
			root := conversionHomes(t)
			if provider == "conversion-test" {
				configureConversionCodexProvider(t)
			}
			from := chatmodel.BackendClaude
			path := filepath.Join("testdata", "carry", "claude-tools.jsonl")
			originalTurns, err := IndexSessionTurns(from, path, 0)
			if err != nil || len(originalTurns) == 0 {
				t.Fatalf("cannot read source conversation: %+v, %v", originalTurns, err)
			}
			ids := map[string]bool{}
			for step, to := range []chatmodel.Backend{"codex", "claude", "codex"} {
				if !t.Run(fmt.Sprintf("step-%d-%s", step+1, to), func(t *testing.T) {
					before, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					converted, err := ConvertSession(from, to, path, root, provider)
					if err != nil {
						t.Fatal(err)
					}
					if ids[converted.ID] {
						t.Fatal("repeated switch reused an earlier native session")
					}
					ids[converted.ID] = true
					assertConvertedNativeResume(t, to, binaries[to], root, converted, provider, false)
					turns, err := IndexSessionTurns(to, converted.Path, 0)
					if err != nil || len(turns) != len(originalTurns) {
						t.Fatalf("switch lost or duplicated turns: %+v, %v", turns, err)
					}
					for i, turn := range turns {
						original := originalTurns[i]
						if turn.Backend != original.Backend || turn.Model != original.Model || turn.ReasoningEffort != original.ReasoningEffort {
							t.Fatalf("switch changed turn %d attribution: %+v, want %+v", i, turn, original)
						}
					}
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("switch changed source history", err)
					}
					path, from = converted.Path, to
				}) {
					return
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
		converted, err := ConvertSession(from, backend, filepath.Join("testdata", "carry", string(from)+"-tools.jsonl"), root, "openai")
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
