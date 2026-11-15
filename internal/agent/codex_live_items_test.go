package agent

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

// A viewer applies live parts as the workspace does: a complete body replaces
// what was streamed for its item, anything else continues it.
func liveItemBodies(events []AgentEvent) map[string]string {
	bodies := map[string]string{}
	for _, event := range events {
		part := event.Part
		if part == nil || part.ID == "" || part.Kind != "tool_result" && part.Kind != "thinking" {
			continue
		}
		if part.Replace {
			bodies[part.ID] = part.Text
		} else {
			bodies[part.ID] += part.Text
		}
	}
	return bodies
}

func TestCodexCompletedItemsRestateStreamedBodiesWithoutLosingOrRepeatingThem(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "restated-items"
	_, events := runFixture(t, newTestRunner(t), request)
	bodies := liveItemBodies(events)
	// Summary sections are streamed one after another and completed as paragraphs.
	if bodies["sections"] != "First section\n\nSecond section" {
		t.Fatalf("completed reasoning repeated or lost its stream: %q", bodies["sections"])
	}
	// A completion without output of its own keeps what the command streamed.
	if bodies["tool"] != "fixture output\n" {
		t.Fatalf("an empty completion changed streamed output: %q", bodies["tool"])
	}
	// A completion with different output is the record.
	if bodies["rerun"] != "final\n" {
		t.Fatalf("completed output did not replace its stream: %q", bodies["rerun"])
	}
}

func TestCodexLocalShellCallIsPairedWithItsOutputByCallID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeTitleRecords(t, path,
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "local_shell_call", "id": "lsh_item", "call_id": "call_shell", "status": "completed", "action": map[string]any{"type": "exec", "command": []any{"ls", "-la"}}}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call_shell", "output": "total 0"}},
	)
	transcript, err := ParseSessionTranscript(chatmodel.BackendCodex, path)
	if err != nil {
		t.Fatal(err)
	}
	var call, result chatmodel.Part
	for _, part := range transcript.Parts {
		switch part.Kind {
		case "tool_use":
			call = part
		case "tool_result":
			result = part
		}
	}
	if call.Name != "shell" || call.ID != "call_shell" || result.ID != call.ID || result.Text != "total 0" {
		t.Fatalf("local shell call and output are not one pair: call=%+v result=%+v", call, result)
	}
	// A record without a call_id is still identified by its own id.
	if parts := codexTranscriptParts(map[string]any{"type": "local_shell_call", "id": "only-id", "action": map[string]any{"command": []any{"pwd"}}}, ""); len(parts) != 1 || parts[0].ID != "only-id" {
		t.Fatalf("fallback identity: %+v", parts)
	}
}

func TestCodexFileChangeMentioningADiffHeaderStillGetsItsOwn(t *testing.T) {
	mention := codexTranscriptParts(decodeItem(t, `{"type":"fileChange","id":"doc","status":"completed","changes":[{"path":"notes.md","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-see the patch\n+see diff --git a/x b/x in the patch"}]}`), "")
	if len(mention) != 2 || !strings.Contains(mention[1].Text, "--- a/notes.md\n+++ b/notes.md\n@@ -1 +1 @@") {
		t.Fatalf("a changed line naming a header left the file without one: %+v", mention)
	}
	if files := mention[0].Native.Files; len(files) != 1 || files[0].Additions != 1 || files[0].Deletions != 1 {
		t.Fatalf("statistics: %+v", files)
	}
	// A patch that brings its header is not given a second one.
	own := codexTranscriptParts(decodeItem(t, `{"type":"fileChange","id":"own","status":"completed","changes":[{"path":"own.txt","kind":{"type":"update"},"diff":"diff --git a/own.txt b/own.txt\n--- a/own.txt\n+++ b/own.txt\n@@ -1 +1 @@\n-a\n+b"}]}`), "")
	if len(own) != 2 || strings.Count(own[1].Text, "--- a/own.txt") != 1 {
		t.Fatalf("existing header was duplicated: %+v", own)
	}
}

func TestCodexRolloutToolEventIsShownOnceWhetherOrNotAResponseItemRecordsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	event := func(id, text string) map[string]any {
		return map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{
			"type": "McpToolCall", "id": id, "server": "docs", "tool": "search", "status": "completed", "arguments": map[string]any{"q": "spool"},
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}},
		}}}
	}
	writeTitleRecords(t, path,
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "docs__search", "call_id": "recorded", "arguments": `{"q":"spool"}`}},
		event("recorded", "from the event"),
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "recorded", "output": "from the response"}},
		event("event-only", "only here"),
	)
	transcript, err := ParseSessionTranscript(chatmodel.BackendCodex, path)
	if err != nil {
		t.Fatal(err)
	}
	calls, results := map[string][]chatmodel.Part{}, map[string][]string{}
	for _, part := range transcript.Parts {
		switch part.Kind {
		case "tool_use":
			calls[part.ID] = append(calls[part.ID], part)
		case "tool_result":
			results[part.ID] = append(results[part.ID], part.Text)
		}
	}
	// The response items show the recorded call; its event only adds the outcome.
	if recorded := calls["recorded"]; len(recorded) != 1 || recorded[0].Native == nil || recorded[0].Native.Status != chatmodel.ToolCompleted || !reflect.DeepEqual(results["recorded"], []string{"from the response"}) {
		t.Fatalf("recorded call: %+v results %v", calls["recorded"], results["recorded"])
	}
	// Nothing else shows the other call, so its event is the call and its result.
	if only := calls["event-only"]; len(only) != 1 || only[0].Name != "docs.search" || !reflect.DeepEqual(results["event-only"], []string{"only here"}) {
		t.Fatalf("a tool recorded only by its event was dropped: %+v results %v", calls["event-only"], results["event-only"])
	}
}

func TestCompactionEndsWhenAToolReportsItsOutcome(t *testing.T) {
	var states []bool
	emit := compactionEvents(func(event AgentEvent) error {
		if event.Type == "compaction" {
			states = append(states, event.Compacting)
		}
		return nil
	})
	for _, event := range []AgentEvent{{Type: "compaction", Compacting: true}, {Type: "tool", Part: &chatmodel.Part{ID: "call", Role: "assistant", Kind: "tool_use"}}} {
		if err := emit(event); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(states, []bool{true, false}) {
		t.Fatalf("a tool outcome left compaction active: %v", states)
	}
}
