package agent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestCompactionStreamState(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, mode := range []string{"compact-stream", "compact-stream legacy-end", "compact-stream no-end"} {
			t.Run(string(backend)+"/"+mode, func(t *testing.T) {
				request := newAgentFixture(t, backend)
				request.Prompt = mode
				result, events := runFixture(t, newTestRunner(t), request)
				var states []bool
				active := false
				for _, event := range events {
					if event.Type == "compaction" {
						states = append(states, event.Compacting)
						active = event.Compacting
						if event.Part != nil || event.Text != "" {
							t.Fatal("compaction leaked into transcript")
						}
					} else if event.Type == "delta" || event.Type == "part" || event.Type == "activity" {
						if active {
							t.Fatal("normal output left compaction active")
						}
					}
					if strings.Contains(event.Text, "INTERNAL COMPACTION") || event.Part != nil && strings.Contains(event.Part.Text, "INTERNAL COMPACTION") {
						t.Fatal("compaction summary was emitted")
					}
				}
				if !reflect.DeepEqual(states, []bool{true, false}) || result.Text != "Hello world" {
					t.Fatalf("invalid compaction state transitions: %v, text=%q", states, result.Text)
				}
			})
		}
	}
}

func TestCompactionEventsPropagateErrors(t *testing.T) {
	want := errors.New("stream closed")
	emit := compactionEvents(func(AgentEvent) error { return want })
	if !errors.Is(emit(AgentEvent{Type: "compaction", Compacting: true}), want) {
		t.Fatal("start callback error lost")
	}
	if !errors.Is(emit(AgentEvent{Type: "delta", Text: "answer"}), want) {
		t.Fatal("implicit end callback error lost")
	}
}

func TestCodexCompactionHistoryHidesConfirmedSummaryAndItsReasoning(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Explain compact behavior"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Normal thought about compact"}]}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","summary_text":["Normal thought about compact"]}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"answer","phase":"final_answer","content":"Normal answer"}}}`,
		`{"type":"response_item","payload":{"type":"message","id":"answer","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Normal answer"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Internal compaction reasoning"}]}}`,
		`{"type":"response_item","payload":{"type":"message","id":"summary","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Internal summary"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"type":"compacted","payload":{"message":"Earlier context:\nInternal summary","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Replacement summary"}]}]}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"ContextCompaction"}}}`,
		`{"type":"response_item","payload":{"type":"context_compaction","text":"Another internal summary"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Next question"}]}}`,
		`{"type":"response_item","payload":{"type":"message","id":"next","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Next answer"}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendCodex, strings.NewReader(raw), "model", false)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, part := range transcript.Parts {
		texts = append(texts, part.Text)
	}
	want := []string{"Explain compact behavior", "Normal thought about compact", "Normal answer", "Next question", "Next answer"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("compaction projection changed real messages: %v", texts)
	}
}

func TestCodexCompactionDoesNotHideUnrelatedOrCorroboratedAnswer(t *testing.T) {
	for _, mode := range []string{"mismatch", "normal-event", "normal-event-after", "no-record"} {
		t.Run(mode, func(t *testing.T) {
			event := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"answer","content":"Keep me"}}}`
			raw := `{"type":"response_item","payload":{"type":"message","id":"answer","role":"assistant","content":[{"type":"output_text","text":"Keep me"}]}}` + "\n"
			if mode == "normal-event" {
				raw = event + "\n" + raw
			} else if mode == "normal-event-after" {
				raw += event + "\n"
			}
			message := "Prefix\nKeep me"
			if mode == "mismatch" {
				message = "Other summary"
			}
			if mode != "no-record" {
				raw += `{"type":"compacted","payload":{"message":"` + strings.ReplaceAll(message, "\n", `\n`) + `"}}`
			}
			transcript, err := parseTranscriptReader(chatmodel.BackendCodex, strings.NewReader(raw), "", false)
			if err != nil || len(transcript.Parts) != 1 || transcript.Parts[0].Text != "Keep me" {
				t.Fatal("normal answer was lost", err)
			}
		})
	}
}

func TestClaudeCompactionHistoryIsNotConversation(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"user","message":{"content":"Please explain compact"}}`,
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"user","isCompactSummary":true,"message":{"content":"Internal summary"}}`,
		`{"type":"assistant","isCompactSummary":true,"message":{"content":"Internal summary"}}`,
		`{"type":"user","message":{"content":"<command-name>/compact</command-name>\n<command-message>compact</command-message>"}}`,
		`{"type":"user","message":{"content":"<local-command-stdout>Compacted</local-command-stdout>"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Normal compact thought"},{"type":"text","text":"Normal answer"}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendClaude, strings.NewReader(raw), "", false)
	if err != nil || len(transcript.Parts) != 3 || transcript.Parts[0].Text != "Please explain compact" || transcript.Parts[1].Kind != "thinking" || transcript.Parts[2].Text != "Normal answer" {
		t.Fatalf("compact metadata was displayed or real text lost: %#v, %v", transcript, err)
	}
}

func TestClaudeCompactionSummaryDoesNotChangeTurnStatusOrMessagePhase(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"user","uuid":"prompt","message":{"content":"Real prompt"}}`,
		`{"type":"assistant","uuid":"progress","parentUuid":"prompt","message":{"id":"answer","content":[{"type":"text","text":"Progress"}],"stop_reason":"tool_use"}}`,
		`{"type":"assistant","uuid":"summary","parentUuid":"progress","isCompactSummary":true,"message":{"id":"answer","model":"compaction-model","content":"Internal summary","stop_reason":"end_turn"}}`,
		`{"type":"assistant","uuid":"tool","parentUuid":"summary","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"main.go"}}],"stop_reason":"tool_use"}}`,
	}, "\n")
	path := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurns(chatmodel.BackendClaude, path, 0)
	if err != nil || len(turns) != 1 || turns[0].Status != chatmodel.StatusRunning || turns[0].Model != "" {
		t.Fatal("internal compaction changed turn metadata", err)
	}
	transcript, err := ReadSessionTurn(chatmodel.BackendClaude, path, turns[0])
	if err != nil || len(transcript.Parts) != 3 || transcript.Parts[1].Phase != "commentary" || transcript.Parts[2].Kind != "tool_use" {
		t.Fatal("internal compaction changed progress phase or parent ownership", err)
	}
}
