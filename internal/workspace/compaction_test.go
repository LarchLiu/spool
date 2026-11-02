package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestProviderOnlyHistoryHidesEmptyTurnsButKeepsAutomaticOutput(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"first"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Real prompt"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Real answer"}]}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"first"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"internal"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\">internal</codex_internal_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Internal compaction reasoning"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Internal summary"}]}}`,
		`{"type":"compacted","payload":{"message":"Context:\nInternal summary"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"internal"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"continuation"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\">internal</codex_internal_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Automatic continuation"}]}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"continuation"}}`,
	}, "\n")
	path := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	chat, err := store.readHistory(chatmodel.Session{ID: "chat", Backend: chatmodel.BackendCodex, NativeSessionPath: path})
	if err != nil || len(chat.Turns) != 2 || len(chat.Transcript.Parts) != 3 {
		t.Fatal("provider-only history left empty turns or lost automatic output", err)
	}
	if chat.Turns[0].Prompt != "Real prompt" || chat.Turns[1].Prompt != "" || chat.Turns[1].Reply != "Automatic continuation" || chat.Turns[1].InputEnd != chat.Turns[1].PartStart {
		t.Fatal("automatic output acquired a fake user prompt")
	}
}

func TestCompactionSnapshotsHaveStateWithoutTranscriptParts(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			variable := "SPOOL_CODEX_BIN"
			if backend == chatmodel.BackendClaude {
				variable = "SPOOL_CLAUDE_BIN"
			}
			t.Setenv(variable, fixture.Binary)
			store := testStore(t)
			chat, err := store.createSession("", "", backend)
			if err != nil {
				t.Fatal(err)
			}
			app := NewApp(store, newTestRunner(t), "", "")
			t.Cleanup(func() { app.Close() })
			var states []bool
			_, err = app.runTurn(context.Background(), chat.ID, "compact-stream", func(event agent.AgentEvent) error {
				if event.Type != "compaction" {
					return nil
				}
				base, _ := store.Get(chat.ID)
				page := historyWindow(app.snapshot(base))
				if len(page.Turns) != 1 || page.Turns[0].Compacting != event.Compacting || len(page.Transcript.Parts) != 1 {
					t.Fatal("snapshot lost transient state or inserted a compact part")
				}
				states = append(states, event.Compacting)
				return nil
			})
			if err != nil || !reflect.DeepEqual(states, []bool{true, false}) {
				t.Fatalf("snapshot state transitions: %v, %v", states, err)
			}
			base, _ := store.Get(chat.ID)
			page := app.snapshot(base)
			if len(page.Turns) != 1 || page.Turns[0].Compacting || page.Turns[0].Status != chatmodel.StatusCompleted {
				t.Fatal("completed turn retained compact state")
			}
		})
	}
}

type compactingFailureRunner struct{ fakeRunner }

func (r *compactingFailureRunner) Run(_ context.Context, _ agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	if err := emit(agent.AgentEvent{Type: "compaction", Compacting: true}); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{}, errors.New("fixture failure during compaction")
}

func TestFailedCompactionClearsRuntimeAndCompletedState(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	app := NewApp(store, &compactingFailureRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	if _, err := app.runTurn(context.Background(), chat.ID, "prompt", nil); err == nil {
		t.Fatal("expected fixture failure")
	}
	base, _ := store.Get(chat.ID)
	page := app.snapshot(base)
	if len(page.Turns) != 1 || page.Turns[0].Compacting || page.Turns[0].Status != chatmodel.StatusFailed {
		t.Fatal("failed run retained compact status")
	}
	if turn, ok := store.runtimeTurn(chat.ID); !ok || turn.Compacting {
		t.Fatal("runtime retained compact status")
	}
}
