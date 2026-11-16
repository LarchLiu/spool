package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

type steeredTurn struct {
	id      string
	result  AgentResult
	request AgentRequest
	events  []AgentEvent
}

// steerDuringTurn runs one fixture turn and steers it once it is under way.
func steerDuringTurn(t *testing.T, backend chatmodel.Backend, prompt string) steeredTurn {
	t.Helper()
	turn := steeredTurn{id: NewSteerID(), request: newAgentFixture(t, backend)}
	turn.request.Prompt = prompt
	runner := newTestRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	steered := make(chan error, 1)
	sent := false
	var err error
	turn.result, err = runner.Run(ctx, turn.request, func(event AgentEvent) error {
		turn.events = append(turn.events, event)
		if sent || (event.Type != "turn" && event.Type != "delta") {
			return nil
		}
		sent = true
		// The turn's own reader delivers the provider's reply to this call.
		go func() {
			steered <- runner.Steer(ctx, SteerRequest{ID: turn.id, ChatID: turn.request.ChatID, Prompt: "change course"})
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if !sent {
		t.Fatal("turn finished before it could be steered")
	}
	if err := <-steered; err != nil {
		t.Fatalf("steer failed: %v", err)
	}
	if err := runner.Steer(ctx, SteerRequest{ChatID: turn.request.ChatID, Prompt: "too late"}); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("steering a finished turn = %v, want ErrNoActiveTurn", err)
	}
	return turn
}

func (turn steeredTurn) logged(t *testing.T) map[string]any {
	t.Helper()
	for _, entry := range fixtureLog(t, turn.request) {
		if entry["method"] == "steer" {
			value, _ := entry["value"].(map[string]any)
			return value
		}
	}
	t.Fatal("provider never received the steering message")
	return nil
}

// settled returns the reply text streamed before the one event of the given
// type that tells what became of the message.
func (turn steeredTurn) settled(t *testing.T, kind string) string {
	t.Helper()
	var settled []AgentEvent
	before, text := "", ""
	for _, event := range turn.events {
		switch event.Type {
		case "delta":
			text += event.Text
		case "steered", "unsteered":
			settled, before = append(settled, event), text
		}
	}
	if len(settled) != 1 || settled[0].Type != kind || settled[0].SteerID != turn.id {
		t.Fatalf("events = %#v, want one %s for %s", settled, kind, turn.id)
	}
	if want := map[string]int{"steered": 1}[kind]; turn.result.Steered != want {
		t.Fatalf("result counts %d messages taken, want %d", turn.result.Steered, want)
	}
	return before
}

func TestCodexSteerJoinsTheRunningTurn(t *testing.T) {
	turn := steerDuringTurn(t, chatmodel.BackendCodex, "steer-wait")
	steer := turn.logged(t)
	params, _ := steer["params"].(map[string]any)
	input, _ := params["input"].([]any)
	if len(input) == 0 {
		t.Fatalf("turn/steer carried no input: %#v", steer)
	}
	first, _ := input[0].(map[string]any)
	if steer["method"] != "turn/steer" || params["expectedTurnId"] != turn.result.TurnID || params["threadId"] != turn.result.SessionID || params["clientUserMessageId"] != turn.id || first["text"] != "change course" {
		t.Fatalf("unexpected turn/steer request: %#v", steer)
	}
	if turn.result.Text != "Hello world" {
		t.Fatalf("steering changed the reply: %q", turn.result.Text)
	}
	turn.settled(t, "steered")
}

// Codex need not name the client message on the item that carries it.
func TestCodexSteerIsRecognizedByItsText(t *testing.T) {
	turn := steerDuringTurn(t, chatmodel.BackendCodex, "steer-wait steer-unnamed")
	turn.settled(t, "steered")
}

func TestClaudeSteerJoinsTheRunningTurn(t *testing.T) {
	turn := steerDuringTurn(t, chatmodel.BackendClaude, "steer-wait")
	steer := turn.logged(t)
	if steer["type"] != "user" || steer["priority"] != "next" || steer["uuid"] != turn.id || nestedString(steer, "message", "content") != "change course" {
		t.Fatalf("unexpected steering message: %#v", steer)
	}
	if turn.result.Text != "Hello world" {
		t.Fatalf("steering changed the reply: %q", turn.result.Text)
	}
	turn.settled(t, "steered")
}

// Claude Code can give a queued message up without any turn taking it.
func TestClaudeSteerThatNoTurnTakesIsReported(t *testing.T) {
	turn := steerDuringTurn(t, chatmodel.BackendClaude, "steer-wait steer-cancelled")
	turn.settled(t, "unsteered")
	if turn.result.Text != "Hello world" {
		t.Fatalf("a cancelled message changed the reply: %q", turn.result.Text)
	}
}

// A message that arrives after the last model request of a turn is answered
// in a further turn, whose output still belongs to this run.
func TestClaudeSteerAfterTheLastRequestKeepsTheFollowingTurn(t *testing.T) {
	turn := steerDuringTurn(t, chatmodel.BackendClaude, "steer-late")
	if turn.result.Text != "Hello world Steered" {
		t.Fatalf("lost the turn that answered the steering message: %q", turn.result.Text)
	}
	if before := turn.settled(t, "steered"); before != "Hello world" {
		t.Fatalf("the message was placed after %q, want it between the two answers", before)
	}
}

func TestSteerWithoutARunningTurn(t *testing.T) {
	runner := newTestRunner(t)
	if err := runner.Steer(context.Background(), SteerRequest{ChatID: "missing", Prompt: "hello"}); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("got %v, want ErrNoActiveTurn", err)
	}
}

// Claude Code records a message sent during a turn as a queued command of
// that turn. It stays one turn, with the message where the model saw it.
func TestClaudeHistoryKeepsSteeringMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	previous := ""
	add := func(typ string, fields map[string]any) {
		t.Helper()
		id := NewSteerID()
		record := map[string]any{"type": typ, "uuid": id, "parentUuid": previous, "timestamp": "2026-11-16T00:00:00Z"}
		for key, value := range fields {
			record[key] = value
		}
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
		previous = id
	}
	message := func(role string, content any) map[string]any {
		return map[string]any{"message": map[string]any{"role": role, "content": content, "model": "claude-test"}}
	}
	queued := func(prompt any, fields map[string]any) map[string]any {
		attachment := map[string]any{"type": "queued_command", "prompt": prompt, "commandMode": "prompt", "origin": map[string]any{"kind": "human"}}
		for key, value := range fields {
			attachment[key] = value
		}
		return map[string]any{"attachment": attachment, "timestamp": "2026-11-16T00:00:05Z"}
	}
	add("user", message("user", "first"))
	add("assistant", message("assistant", []any{map[string]any{"type": "tool_use", "id": "call-1", "name": "Read", "input": map[string]any{"file_path": "x"}}}))
	add("user", message("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": "tool output"}}))
	add("attachment", queued("change course", nil))
	add("attachment", queued([]any{map[string]any{"type": "text", "text": "and look at this"}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGk="}}}, nil))
	// A message from stdin may carry no origin at all.
	add("attachment", queued("from stdin", map[string]any{"origin": nil}))
	add("attachment", queued("<task-notification>done</task-notification>", map[string]any{"commandMode": "task-notification", "origin": map[string]any{"kind": "task-notification"}}))
	add("attachment", queued("a reply from another agent", map[string]any{"origin": map[string]any{"kind": "peer"}}))
	add("attachment", queued("an intent the client forwarded", map[string]any{"isMeta": true, "origin": map[string]any{"kind": "unclassified"}}))
	add("attachment", map[string]any{"attachment": map[string]any{"type": "todo_reminder", "prompt": "not a message"}})
	add("attachment", map[string]any{"isSidechain": true, "attachment": queued("said to a subagent", nil)["attachment"]})
	add("assistant", message("assistant", []any{map[string]any{"type": "text", "text": "done"}}))
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurns(chatmodel.BackendClaude, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("a steering message split its turn: %+v", turns)
	}
	transcript, err := ReadSessionTurn(chatmodel.BackendClaude, path, turns[0])
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, part := range transcript.Parts {
		got = append(got, part.Role+":"+part.Kind+":"+part.Text)
		if part.Role == "user" && part.Text != "first" && part.Timestamp != time.Date(2026, 11, 16, 0, 0, 5, 0, time.UTC).UnixMilli() {
			t.Fatalf("steering message %q lost the time it was sent: %d", part.Text, part.Timestamp)
		}
	}
	want := []string{
		"user:text:first",
		"assistant:tool_use:{\n  \"file_path\": \"x\"\n}",
		"tool:tool_result:tool output",
		"user:text:change course",
		"user:text:and look at this",
		"user:image:[image]",
		"user:text:from stdin",
		"assistant:text:done",
	}
	if len(got) != len(want) {
		t.Fatalf("parts = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}
