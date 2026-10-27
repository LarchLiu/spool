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

func TestCodexGoalUsesNativeRPCWithoutControlTurns(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "/goal fix all tests"
	// Historical files must not be appended to slash commands.
	request.Files = []chatmodel.AgentAttachment{{Attachment: chatmodel.Attachment{Name: "old.txt"}, Path: "/old/file"}}
	runner := newTestRunner(t)
	result, _ := runFixture(t, runner, request)
	request.NativeSessionID = result.SessionID
	request.Prompt = "/goal"
	result, _ = runFixture(t, runner, request)
	if !strings.Contains(result.Text, `"objective": "fix all tests"`) || !strings.Contains(result.Text, `"status": "active"`) {
		t.Fatalf("native state missing: %#v", result)
	}
	request.Prompt = "/goal clear"
	result, _ = runFixture(t, runner, request)
	if result.Text != "Goal cleared." {
		t.Fatal(result.Text)
	}
	request.Prompt = "/goal"
	result, _ = runFixture(t, runner, request)
	if result.Text != "No goal set." {
		t.Fatal(result.Text)
	}
	if fixtureCount(t, request, "turn/start") != 1 || fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("control commands started model turns or a redundant CLI process")
	}
	set := false
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		params, _ := value["params"].(map[string]any)
		switch entry["method"] {
		case "thread/goal/set", "thread/goal/clear":
			if params["origin"] != "user" || params["threadId"] != result.SessionID {
				t.Fatalf("invalid mutation: %#v", params)
			}
			if entry["method"] == "thread/goal/set" {
				set = true
			}
		case "turn/start":
			if !set {
				t.Fatal("started work before saving native goal")
			}
			input := params["input"].([]any)
			if input[0].(map[string]any)["text"] != "fix all tests" {
				t.Fatalf("goal became an ordinary slash prompt: %#v", input)
			}
		}
	}
}

func TestClaudeGoalIsNativeLocalCommand(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Model = "test-model"
	request.Prompt = "/goal fix tests"
	runner := newTestRunner(t)
	result, _ := runFixture(t, runner, request)
	request.NativeSessionID = result.SessionID
	for _, tt := range []struct{ prompt, text string }{
		{"/goal", "fix tests"}, {"/goal stop", "Goal cleared."}, {"/goal", "No goal set."},
	} {
		request.Prompt = tt.prompt
		request.Files = []chatmodel.AgentAttachment{{Attachment: chatmodel.Attachment{Name: "old.txt"}, Path: "/old/file"}}
		result, _ = runFixture(t, runner, request)
		if result.Text != tt.text || result.Model == "<synthetic>" || len(result.Activities) != 0 {
			t.Fatalf("local command created work or lost model: %#v", result)
		}
	}
	if fixtureCount(t, request, "user") != 4 || fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("unexpected extra goal messages or processes")
	}
	var prompts []string
	for _, entry := range fixtureLog(t, request) {
		if entry["method"] == "user" {
			value, _ := entry["value"].(map[string]any)
			prompts = append(prompts, nestedString(value, "message", "content"))
		}
	}
	if strings.Join(prompts, "|") != "/goal fix tests|/goal|/goal clear|/goal" {
		t.Fatalf("commands were rewritten as prompts: %#v", prompts)
	}
}

func TestUnsupportedNativeGoalNeverFallsBackToPrompt(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "unsupported-goal"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			request.Prompt = "/goal fix tests"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := newTestRunner(t).Run(ctx, request, nil)
			if err == nil || !strings.Contains(err.Error(), "no prompt fallback") {
				t.Fatalf("missing native support error: %v", err)
			}
			if fixtureCount(t, request, "turn/start") != 0 || fixtureCount(t, request, "user") != 0 {
				t.Fatal("unsupported command reached model")
			}
		})
	}
}

func TestGoalRejectsNewAttachmentsBeforeStartingCLI(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "/goal fix tests"
	request.Attachments = []chatmodel.AgentAttachment{{Attachment: chatmodel.Attachment{Name: "new.txt"}, Path: "/new/file"}}
	_, err := newTestRunner(t).Run(context.Background(), request, nil)
	if err == nil || !strings.Contains(err.Error(), "attachments") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(request.WorkingDirectory, "fixture-log.jsonl")); !os.IsNotExist(err) {
		t.Fatal("CLI started for invalid command")
	}
}
