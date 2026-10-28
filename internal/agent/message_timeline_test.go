package agent

import (
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestProviderMessageTimeline(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, mode := range []string{"message-timeline", "message-timeline full-only", "message-timeline result-only"} {
			t.Run(string(backend)+"/"+mode, func(t *testing.T) {
				request := newAgentFixture(t, backend)
				request.Prompt = mode
				result, events := runFixture(t, newTestRunner(t), request)
				if result.Text != "Checking.One more check.Done." {
					t.Fatalf("lost or duplicated message text: %q", result.Text)
				}
				var transcript chatmodel.Transcript
				for _, event := range events {
					switch event.Type {
					case "delta":
						if event.Part == nil || event.Part.ID == "" {
							t.Fatalf("text delta lost its message identity: %#v", event)
						}
						chatmodel.AppendStreamText(&transcript, *event.Part)
					case "message":
						for i := range transcript.Parts {
							if transcript.Parts[i].ID == event.Part.ID {
								transcript.Parts[i].Phase = event.Part.Phase
							}
						}
					case "activity":
						if event.Part != nil {
							chatmodel.AppendPart(&transcript, *event.Part)
						}
					}
				}
				var messages, phases []string
				toolSinceText := false
				for _, part := range transcript.Parts {
					if part.Kind == "tool_use" {
						toolSinceText = true
					}
					if part.Kind != "text" {
						continue
					}
					if len(messages) > 0 && !toolSinceText {
						t.Fatalf("missing interleaved tool: %#v", transcript.Parts)
					}
					messages, phases = append(messages, part.Text), append(phases, part.Phase)
					toolSinceText = false
				}
				if !reflect.DeepEqual(messages, []string{"Checking.", "One more check.", "Done."}) || !reflect.DeepEqual(phases, []string{"commentary", "commentary", "final_answer"}) {
					t.Fatalf("timeline lost message boundaries or phases: %#v", transcript.Parts)
				}
			})
		}
	}
}

func TestCodexHistoryPreservesPhasesWithoutDuplicateEventMessages(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"progress","phase":"commentary","content":"Same text"}}}`,
		`{"type":"response_item","payload":{"type":"message","id":"progress","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Same text"}]}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"final","phase":"final_answer","content":"Same text"}}}`,
		`{"type":"response_item","payload":{"type":"message","id":"final","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Same text"}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendCodex, strings.NewReader(raw), "test-model", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Parts) != 2 || transcript.Parts[0].ID != "progress" || transcript.Parts[0].Phase != "commentary" || transcript.Parts[1].ID != "final" || transcript.Parts[1].Phase != "final_answer" {
		t.Fatalf("message phases lost or messages duplicated: %#v", transcript.Parts)
	}
}

func TestCodexHistoryBackfillsPhaseFromDuplicateResponse(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"answer","content":[{"type":"Text","text":"First"},{"type":"Text","text":"Last"}]}}}`,
		`{"type":"response_item","payload":{"type":"message","id":"answer","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"First"},{"type":"output_text","text":"Last"}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendCodex, strings.NewReader(raw), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Parts) != 1 || transcript.Parts[0].Text != "First\n\nLast" || transcript.Parts[0].Phase != "final_answer" {
		t.Fatalf("duplicate message lost its final phase or repeated a block: %#v", transcript.Parts)
	}
}

func TestClaudeHistoryPreservesMessagePhases(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"assistant","message":{"id":"progress","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"read","name":"Read","input":{"file_path":"main.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"read","content":"Contents"}]}}`,
		`{"type":"assistant","message":{"id":"answer","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendClaude, strings.NewReader(raw), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Parts) != 4 || transcript.Parts[0].ID != "progress" || transcript.Parts[0].Phase != "commentary" || transcript.Parts[3].ID != "answer" || transcript.Parts[3].Phase != "final_answer" {
		t.Fatalf("Claude phases or tool order lost: %#v", transcript.Parts)
	}
}

func TestClaudeResultOnlyAnswerIsNotDeduplicatedAgainstProgressSuffix(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Prompt = "message-timeline result-only-suffix"
	result, events := runFixture(t, newTestRunner(t), request)
	if result.Text != "Checking.One more check. Done.Done." {
		t.Fatalf("result-only answer was dropped because progress ended with the same text: %q", result.Text)
	}
	var final *chatmodel.Part
	for _, event := range events {
		if event.Type == "delta" && event.Part.Phase == "final_answer" {
			final = event.Part
		}
	}
	if final == nil || final.Text != "Done." || final.ID == "message-1" {
		t.Fatalf("result-only final was merged into progress: %#v", final)
	}
}

func TestClaudeHistoryBackfillsPhaseAcrossMessageBlocks(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"assistant","message":{"id":"message","role":"assistant","content":[{"type":"text","text":"First block"}]}}`,
		`{"type":"assistant","message":{"id":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Last block"}]}}`,
	}, "\n")
	transcript, err := parseTranscriptReader(chatmodel.BackendClaude, strings.NewReader(raw), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Parts) != 2 || transcript.Parts[0].Phase != "final_answer" || transcript.Parts[1].Phase != "final_answer" {
		t.Fatalf("earlier answer block was mistaken for progress: %#v", transcript.Parts)
	}
}
