package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestClaudeEffortChangesReuseProcessAndSurviveResume(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Model = "test-claude"
	runner := newTestRunner(t)
	runner.IdleTimeout = 300 * time.Millisecond
	levels := []string{"high", "low", "low", "", "max"}
	for _, effort := range levels {
		request.ReasoningEffort = effort
		result, events := runFixture(t, runner, request)
		if result.ReasoningEffort != effort {
			t.Fatalf("result lost effort: %#v", result)
		}
		for _, event := range events {
			if event.Type == "model" && event.ReasoningEffort != effort {
				t.Fatalf("model update lost effort: %#v", event)
			}
		}
		request.NativeSessionID = result.SessionID
	}
	if fixtureCount(t, request, "spawn") != 1 || fixtureCount(t, request, "user") != len(levels) {
		t.Fatal("effort selection restarted the process or sent extra prompts")
	}
	var changes []any
	var applied []string
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		if nestedString(value, "request", "subtype") == "apply_flag_settings" {
			control, _ := value["request"].(map[string]any)
			settings, _ := control["settings"].(map[string]any)
			changes = append(changes, settings["effortLevel"])
		}
		if entry["method"] == "user" {
			applied = append(applied, stringValue(entry["effort"]))
		}
	}
	if !reflect.DeepEqual(changes, []any{"high", "low", nil, "max"}) || !reflect.DeepEqual(applied, []string{"high", "low", "low", "high", "max"}) {
		t.Fatalf("settings were not applied before input: changes=%#v applied=%#v", changes, applied)
	}
	waitProcessExit(t, currentProcess(t, runner, request.ChatID))
	request.ReasoningEffort = "low"
	runFixture(t, runner, request)
	if fixtureCount(t, request, "spawn") != 2 {
		t.Fatal("expected a new process after idle release")
	}
	entries := fixtureLog(t, request)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["method"] == "user" {
			if entries[i]["effort"] != "low" {
				t.Fatal("resumed turn lost its selected effort")
			}
			break
		}
	}
}

func TestClaudeRejectedOrClampedEffortNeverSendsUserMessage(t *testing.T) {
	for _, model := range []string{"reject-effort", "clamp-effort"} {
		t.Run(model, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendClaude)
			request.Model, request.ReasoningEffort = model, "high"
			_, err := newTestRunner(t).Run(context.Background(), request, nil)
			if err == nil || !strings.Contains(err.Error(), "reasoning level") {
				t.Fatalf("effort failure not reported: %v", err)
			}
			if fixtureCount(t, request, "user") != 0 {
				t.Fatal("message was sent without the selected effort")
			}
		})
	}
}
