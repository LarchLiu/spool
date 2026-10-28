package workspace

import (
	"context"
	"reflect"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestLiveMessageTimelineKeepsLatePhasesAndToolOrder(t *testing.T) {
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
			seenFinal := false
			_, err = app.runTurn(context.Background(), chat.ID, "message-timeline", func(event agent.AgentEvent) error {
				app.liveMu.Lock()
				defer app.liveMu.Unlock()
				turn := app.live[chat.ID]
				var messages, phases []string
				toolSinceText := false
				for _, part := range turn.Parts {
					if part.Kind == "tool_use" {
						toolSinceText = true
					}
					if part.Role != "assistant" || part.Kind != "text" {
						continue
					}
					if len(messages) > 0 && !toolSinceText {
						t.Errorf("message was moved away from its tools: %#v", turn.Parts)
					}
					messages, phases = append(messages, part.Text), append(phases, part.Phase)
					toolSinceText = false
				}
				if reflect.DeepEqual(messages, []string{"Checking.", "One more check.", "Done."}) && reflect.DeepEqual(phases, []string{"commentary", "commentary", "final_answer"}) {
					seenFinal = true
				}
				return nil
			})
			if err != nil || !seenFinal {
				t.Fatalf("live snapshot never contained the complete phased timeline: final=%v error=%v", seenFinal, err)
			}
		})
	}
}
