package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestRunnerStreamsAndReusesCLIProcess(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			for turn := 0; turn < 3; turn++ {
				result, events := runFixture(t, runner, request)
				if result.Text != "Hello world" || result.Model != "test-model" || result.SessionID != "native-session-123" {
					t.Fatalf("unexpected result: %#v", result)
				}
				var streamed strings.Builder
				preparations := 0
				for _, event := range events {
					if event.Type == "delta" {
						streamed.WriteString(event.Text)
					}
					if event.Text == "Preparing isolated workspace" {
						preparations++
					}
				}
				if streamed.String() != result.Text {
					t.Fatalf("streamed text = %q, result = %q", streamed.String(), result.Text)
				}
				if preparations != 0 || len(result.Activities) != 1 {
					t.Fatalf("unexpected activities: preparations=%d, result=%#v", preparations, result)
				}
				if backend == chatmodel.BackendCodex {
					thinking, toolResult := false, false
					for _, event := range events {
						if event.Type != "part" || event.Part == nil {
							continue
						}
						thinking = thinking || event.Part.Kind == "thinking"
						toolResult = toolResult || event.Part.Kind == "tool_result"
					}
					if !thinking || !toolResult {
						t.Fatalf("Codex live events lost normalized parts: thinking=%v tool_result=%v events=%#v", thinking, toolResult, events)
					}
				}
				if info, err := os.Stat(request.WorkingDirectory); err != nil || !info.IsDir() {
					t.Fatalf("existing working directory is unavailable: %v", err)
				}
				request = nextFixtureTurn(request, result.SessionID)
			}
			if got := fixtureCount(t, request, "spawn"); got != 1 {
				t.Fatalf("started %d processes for 3 turns, want 1", got)
			}
			initialized := 0
			for _, entry := range fixtureLog(t, request) {
				value, _ := entry["value"].(map[string]any)
				if entry["method"] == "initialize" || (entry["method"] == "control_request" && nestedString(value, "request", "subtype") == "initialize") {
					initialized++
				}
			}
			if got := initialized; got != 1 {
				t.Fatalf("initialized %d times, want 1", got)
			}
			if backend == chatmodel.BackendCodex && fixtureCount(t, request, "thread/start") != 1 {
				t.Fatal("Codex thread was created more than once")
			}
			for _, name := range []string{"AGENTS.md", "CLAUDE.md", "skills", ".agents", ".claude", "agents", "turns"} {
				if _, err := os.Lstat(filepath.Join(request.WorkingDirectory, name)); !os.IsNotExist(err) {
					t.Errorf("unexpected injected workspace resource %s: %v", name, err)
				}
			}
		})
	}
}

func TestProviderEventsUseMagpieTranscriptParts(t *testing.T) {
	codex := codexTranscriptPart(map[string]any{
		"type": "commandExecution", "command": []any{"go", "test", "./..."},
	}, "gpt-6")
	if codex.Role != "assistant" || codex.Kind != "tool_use" || codex.Name != "shell" || codex.Text != "go test ./..." || codex.Model != "gpt-6" {
		t.Fatalf("unexpected Codex part: %#v", codex)
	}
	reasoning := codexTranscriptParts(map[string]any{
		"type": "reasoning", "summary": []any{map[string]any{"text": "Checking the fixture"}},
	}, "gpt-6")
	if len(reasoning) != 1 || reasoning[0].Role != "assistant" || reasoning[0].Kind != "thinking" || reasoning[0].Model != "gpt-6" {
		t.Fatalf("unexpected Codex reasoning part: %#v", reasoning)
	}
	reasoningContent := codexTranscriptParts(map[string]any{
		"type": "Reasoning", "summary": []any{}, "content": []any{map[string]any{"reasoning_text": "Actual reasoning"}},
	}, "gpt-6")
	if len(reasoningContent) != 1 || reasoningContent[0].Kind != "thinking" || reasoningContent[0].Text != "Actual reasoning" {
		t.Fatalf("Codex reasoning content was not preserved: %#v", reasoningContent)
	}
	command := codexTranscriptParts(map[string]any{
		"type": "CommandExecution", "id": "command-1", "command": []any{"sh", "-c", "printf result"}, "aggregated_output": "result",
	}, "gpt-6")
	if len(command) != 2 || command[0].Kind != "tool_use" || command[1].Role != "tool" || command[1].Text != "result" {
		t.Fatalf("Codex command output was not preserved: %#v", command)
	}
	fileChange := codexTranscriptParts(map[string]any{
		"type": "FileChange", "title": "Updated main.go", "changes": []any{map[string]any{"path": "main.go", "diff": "@@ -1 +1 @@\n-old\n+new"}},
	}, "gpt-6")
	if len(fileChange) != 2 || fileChange[1].Kind != "tool_result" || !strings.Contains(fileChange[1].Text, "+++ b/main.go") {
		t.Fatalf("Codex file diff was not preserved: %#v", fileChange)
	}
	claude := claudeToolResultPart(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "read complete"}},
	})
	if claude.Role != "tool" || claude.Kind != "tool_result" || claude.Name != "" || claude.Text != "read complete" {
		t.Fatalf("unexpected Claude part: %#v", claude)
	}
	claudeCall := claudeToolUsePart(map[string]any{
		"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "README.md"},
	}, "claude-sonnet")
	if claudeCall.Role != "assistant" || claudeCall.Kind != "tool_use" || claudeCall.Name != "Read" || claudeCall.Model != "claude-sonnet" || !strings.Contains(claudeCall.Text, `"file_path": "README.md"`) {
		t.Fatalf("unexpected Claude tool-use part: %#v", claudeCall)
	}
}

func TestCodexLiveFileChangesMatchNormalizedHistoryShape(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "file-change"
	result, events := runFixture(t, newTestRunner(t), request)
	if result.Text != "Hello world" {
		t.Fatalf("unexpected result: %#v", result)
	}
	var toolUses, results []chatmodel.Part
	for _, event := range events {
		if event.Part == nil {
			continue
		}
		switch event.Part.Kind {
		case "tool_use":
			toolUses = append(toolUses, *event.Part)
		case "tool_result":
			results = append(results, *event.Part)
		}
	}
	if len(toolUses) != 2 || len(results) != 2 {
		t.Fatalf("live Codex parts = tool uses %d, results %d: %#v", len(toolUses), len(results), events)
	}
	var foundDiff bool
	for _, part := range results {
		if strings.Contains(part.Text, "+++ b/main.go") && strings.Contains(part.Text, "+new") {
			foundDiff = true
		}
	}
	if !foundDiff {
		t.Fatalf("live Codex file diff was not preserved: %#v", results)
	}
}

func TestClaudeLiveThinkingAndStructuredDiffMatchHistoryShape(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Prompt = "thinking-tool structured-diff"
	result, events := runFixture(t, newTestRunner(t), request)
	if result.Text != "Hello world" {
		t.Fatalf("unexpected result: %#v", result)
	}
	var thought, diff bool
	for _, event := range events {
		if event.Part == nil {
			continue
		}
		if event.Part.Kind == "thinking" && strings.Contains(event.Part.Text, "Fixture thought") {
			thought = true
		}
		if event.Part.Kind == "tool_result" && strings.Contains(event.Part.Text, "+++ b/main.go") && strings.Contains(event.Part.Text, "+new") {
			diff = true
		}
	}
	if !thought || !diff {
		t.Fatalf("live Claude parts lost thinking or structured diff: thinking=%v diff=%v events=%#v", thought, diff, events)
	}
}

func TestCodexAgentMessageEventFillsMissingAssistantText(t *testing.T) {
	transcript := chatmodel.Transcript{Parts: []chatmodel.Part{}}
	history := newCodexHistory()
	parts := history.addEvent(map[string]any{
		"type": "item_completed",
		"item": map[string]any{
			"type":    "AgentMessage",
			"content": []any{map[string]any{"type": "Text", "text": "native final answer"}},
		},
	}, "gpt-6", &transcript)
	if len(parts) != 1 || parts[0].Role != "assistant" || parts[0].Kind != "text" || parts[0].Text != "native final answer" {
		t.Fatalf("unexpected AgentMessage fallback: %#v", parts)
	}
	for _, part := range parts {
		chatmodel.AppendPart(&transcript, part)
	}
	if duplicate := history.addEvent(map[string]any{
		"type": "item_completed",
		"item": map[string]any{"type": "AgentMessage", "content": "native final answer"},
	}, "gpt-6", &transcript); len(duplicate) != 0 {
		t.Fatalf("duplicate AgentMessage was emitted: %#v", duplicate)
	}
}

func TestBundledSanitizedSessionFixturesNormalizeToSharedTranscriptParts(t *testing.T) {
	fixtureRoot := bundledFixtureRoot(t)

	claudeTranscript, err := ParseSessionTranscript(chatmodel.BackendClaude, filepath.Join(fixtureRoot, "claude-native-replay.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(claudeTranscript.Parts) != 3 {
		t.Fatalf("Claude fixture parts = %#v, want three assistant text parts", claudeTranscript.Parts)
	}
	for _, part := range claudeTranscript.Parts {
		if part.Role != "assistant" || part.Kind != "text" || part.Model != "claude-opus-5-5" || part.Text != "fixture" {
			t.Fatalf("unexpected Claude fixture part: %#v", part)
		}
	}

	codexTranscript, err := ParseSessionTranscript(chatmodel.BackendCodex, filepath.Join(fixtureRoot, "traces", "codex-real-sanitized.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]int)
	for _, part := range codexTranscript.Parts {
		kinds[part.Role+"/"+part.Kind]++
	}
	wantKinds := map[string]int{
		"user/text":          3,
		"assistant/text":     3,
		"assistant/tool_use": 4,
		"tool/tool_result":   4,
	}
	for kind, want := range wantKinds {
		if kinds[kind] != want {
			t.Errorf("Codex fixture produced %s=%d, want %d: %#v", kind, kinds[kind], want, kinds)
		}
	}
}

func TestClaudeToolResultKeepsStructuredFileDiff(t *testing.T) {
	fixtureRoot := bundledFixtureRoot(t)
	path := filepath.Join(fixtureRoot, "carry", "claude-tools.jsonl")
	transcript, err := ParseSessionTranscript(chatmodel.BackendClaude, path)
	if err != nil {
		t.Fatal(err)
	}
	var found, created bool
	for _, part := range transcript.Parts {
		if part.Kind == "tool_result" && strings.Contains(part.Text, "@@ -4,3 +4,7 @@") {
			found = strings.Contains(part.Text, "+def sub(a, b):")
		}
		if part.Kind == "tool_result" && strings.Contains(part.Text, "@@ -0,0 +1,") {
			created = strings.Contains(part.Text, "+++ b/")
		}
	}
	if !found {
		t.Fatalf("Claude structured patch was not preserved: %#v", transcript.Parts)
	}
	if !created {
		t.Fatalf("Claude create result was not converted to a diff: %#v", transcript.Parts)
	}
}

func bundledFixtureRoot(t *testing.T) string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate agent test source")
	}
	root := filepath.Join(filepath.Dir(source), "testdata")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("bundled session testdata is unavailable at %s: %v", root, err)
	}
	return root
}

func TestBundledToolFixturesCoverProviderToolShapes(t *testing.T) {
	root := bundledFixtureRoot(t)
	codex, err := ParseSessionTranscript(chatmodel.BackendCodex, filepath.Join(root, "carry", "codex-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	seenCodex := map[string]bool{}
	for _, part := range codex.Parts {
		if part.Kind == "tool_use" {
			seenCodex[part.Name] = true
		}
	}
	if !seenCodex["exec_command"] {
		t.Errorf("Codex fixture did not preserve %q tool", "exec_command")
	}
	tracedCodex, err := ParseSessionTranscript(chatmodel.BackendCodex, filepath.Join(root, "traces", "codex-real-sanitized.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	seenCustomExec := false
	for _, part := range tracedCodex.Parts {
		if part.Kind == "tool_use" && part.Name == "exec" {
			seenCustomExec = true
			break
		}
	}
	if !seenCustomExec {
		t.Errorf("sanitized Codex fixture did not preserve custom %q tool", "exec")
	}
	claude, err := ParseSessionTranscript(chatmodel.BackendClaude, filepath.Join(root, "carry", "claude-tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	seenClaude := map[string]bool{}
	for _, part := range claude.Parts {
		if part.Kind == "tool_use" {
			seenClaude[part.Name] = true
		}
	}
	for _, name := range []string{"Bash", "Write", "Read", "Edit"} {
		if !seenClaude[name] {
			t.Errorf("Claude fixture did not preserve %q tool", name)
		}
	}

	paginated, err := ParseSessionTranscript(chatmodel.BackendCodex, filepath.Join(root, "codex-native-paginated.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	resultCount := 0
	for _, part := range paginated.Parts {
		if part.Kind == "tool_result" {
			resultCount++
		}
	}
	if resultCount != 3 {
		t.Fatalf("paginated Codex fixture tool results = %d, want 3", resultCount)
	}
}

func TestParseSessionTranscriptRejectsUnsupportedInputs(t *testing.T) {
	if _, err := ParseSessionTranscript(chatmodel.Backend("pi"), "unused"); err == nil {
		t.Fatal("unsupported session backend was accepted")
	}
	if _, err := ParseSessionTranscript(chatmodel.BackendClaude, ""); err == nil {
		t.Fatal("empty session path was accepted")
	}
}

func TestRunnerPassesUserTextWithoutPromptInjection(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			prompts := []string{"  Keep leading and trailing spaces.  \n", "Line one\nLine two: \"quoted\" <text>", "\u53ea\u53d1\u9001\u6211\u8f93\u5165\u7684\u5185\u5bb9"}
			for _, prompt := range prompts {
				request.Prompt = prompt
				result, _ := runFixture(t, runner, request)
				request = nextFixtureTurn(request, result.SessionID)
			}
			var inputs []string
			for _, entry := range fixtureLog(t, request) {
				value, _ := entry["value"].(map[string]any)
				params, _ := value["params"].(map[string]any)
				switch entry["method"] {
				case "spawn":
					args, _ := entry["args"].([]any)
					for _, arg := range args {
						if strings.Contains(stringValue(arg), "system-prompt") {
							t.Fatalf("injected system prompt argument: %q", arg)
						}
					}
				case "thread/start", "thread/resume":
					assertNoCodexPromptOverrides(t, params)
				case "turn/start":
					assertNoCodexPromptOverrides(t, params)
					input, _ := params["input"].([]any)
					if len(input) != 1 {
						t.Fatalf("unexpected input attachments: %#v", input)
					}
					part, _ := input[0].(map[string]any)
					if part["type"] != "text" {
						t.Fatalf("unexpected input type: %#v", part)
					}
					inputs = append(inputs, stringValue(part["text"]))
				case "user":
					inputs = append(inputs, nestedString(value, "message", "content"))
				}
			}
			if len(inputs) != len(prompts) {
				t.Fatalf("sent %d user inputs, want %d", len(inputs), len(prompts))
			}
			for i, prompt := range prompts {
				if inputs[i] != prompt {
					t.Errorf("turn %d changed user input: got %q, want %q", i, inputs[i], prompt)
				}
			}
		})
	}
}

func assertNoCodexPromptOverrides(t *testing.T, params map[string]any) {
	t.Helper()
	for _, key := range []string{"developerInstructions", "baseInstructions", "systemPrompt"} {
		if _, present := params[key]; present {
			t.Errorf("unexpected prompt override %s: %#v", key, params[key])
		}
	}
}

func TestCodexUsesAppServerSandboxAndFiltersReplayedTurns(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.NativeSessionID = "existing-native-session"
	request.Prompt = "multiple-items"
	result, _ := runFixture(t, newTestRunner(t), request)
	if result.Text != "Hello world More" || strings.Contains(result.Text, "STALE") || result.SessionID != request.NativeSessionID {
		t.Fatalf("unexpected resumed output: %#v", result)
	}
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		params, _ := value["params"].(map[string]any)
		switch entry["method"] {
		case "spawn":
			args, _ := entry["args"].([]any)
			var joined []string
			for _, arg := range args {
				joined = append(joined, stringValue(arg))
			}
			if !strings.Contains(strings.Join(joined, " "), "app-server --listen stdio://") {
				t.Fatalf("not using app-server: %#v", args)
			}
		case "thread/resume":
			assertNoCodexPromptOverrides(t, params)
			if params["threadId"] != request.NativeSessionID || params["sandbox"] != "workspace-write" || params["approvalPolicy"] != "on-request" || params["approvalsReviewer"] != "user" {
				t.Fatalf("unexpected resume parameters: %#v", params)
			}
		case "turn/start":
			policy, _ := params["sandboxPolicy"].(map[string]any)
			if policy["type"] != "workspaceWrite" || policy["networkAccess"] != false {
				t.Fatalf("unsafe sandbox parameters: %#v", policy)
			}
		}
	}
}

func TestProviderFullRepliesWithoutDeltas(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "full-only"
			result, events := runFixture(t, newTestRunner(t), request)
			if result.Text != "Hello world" {
				t.Fatalf("full reply was not captured: %#v", result)
			}
			var streamed strings.Builder
			for _, event := range events {
				if event.Type == "delta" {
					streamed.WriteString(event.Text)
				}
			}
			if streamed.String() != result.Text {
				t.Fatalf("full reply was not forwarded to the browser: %q", streamed.String())
			}
		})
	}
}

func TestProviderLargeRepliesPreserveAllTextWithoutDuplication(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, mode := range []string{"large-reply", "large-reply full-only"} {
			t.Run(string(backend)+"/"+mode, func(t *testing.T) {
				request := newAgentFixture(t, backend)
				request.Prompt = mode
				result, events := runFixture(t, newTestRunner(t), request)
				var streamed strings.Builder
				for _, event := range events {
					if event.Type == "delta" {
						streamed.WriteString(event.Text)
					}
				}
				if result.Text != strings.Repeat("x", 900_000) || streamed.String() != result.Text {
					t.Fatalf("large reply was truncated or duplicated: result=%d streamed=%d", len(result.Text), streamed.Len())
				}
			})
		}
	}
}

func TestCodexQueuesNotificationsBeforeTurnStartAcknowledgement(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "queued-delta"
	result, _ := runFixture(t, newTestRunner(t), request)
	if result.Text != "Queued Hello world" {
		t.Fatalf("lost or duplicated an early delta: %q", result.Text)
	}
}

func TestProviderPermissionsAreDenied(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "permission-test"
			runFixture(t, newTestRunner(t), request)
			found := false
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] != "permission-reply" {
					continue
				}
				found = true
				value, _ := entry["value"].(map[string]any)
				if backend == chatmodel.BackendCodex && nestedString(value, "result", "decision") != "decline" {
					t.Fatalf("Codex permission not denied: %#v", value)
				}
				if backend == chatmodel.BackendClaude && nestedString(value, "response", "response", "behavior") != "deny" {
					t.Fatalf("Claude permission not denied: %#v", value)
				}
			}
			if !found {
				t.Fatal("permission reply missing")
			}
		})
	}
}

func TestRunnerHonorsExplicitCancellationAndKeepsPartialReply(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "block-task"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timer := time.AfterFunc(8*time.Second, cancel)
			defer timer.Stop()
			result, err := newTestRunner(t).Run(ctx, request, func(event AgentEvent) error {
				if event.Type == "delta" {
					cancel()
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "agent stopped") || result.Text != "Partial reply" {
				t.Fatalf("cancel lost partial response: %#v, error=%v", result, err)
			}
		})
	}
}
