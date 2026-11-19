package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestCodexNativeSkillInputKeepsTheRequestWithoutExposingInstructions(t *testing.T) {
	const instructions = "<skill>\n<name>gh</name>\n<path>/skills/gh/SKILL.md</path>\nInternal skill instructions\n</skill>"
	const request = "Show the latest release"
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-boundaries", true: "explicit-boundaries"}[explicit], func(t *testing.T) {
			var records []map[string]any
			add := func(typ string, payload map[string]any) {
				records = append(records, map[string]any{"type": typ, "payload": payload})
			}
			message := func(role, text string) {
				add("response_item", map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": "input_text", "text": text}}})
			}
			if explicit {
				add("event_msg", map[string]any{"type": "task_started", "turn_id": "skill-turn"})
			}
			message("user", request)
			message("user", instructions)
			message("assistant", "Release v1.2.3")
			if explicit {
				add("event_msg", map[string]any{"type": "task_complete", "turn_id": "skill-turn"})
				add("event_msg", map[string]any{"type": "task_started", "turn_id": "continue-turn"})
			}
			message("user", "Continue")
			message("assistant", "Continued")
			var data []byte
			for _, record := range records {
				line, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				data = append(append(data, line...), '\n')
			}
			path := filepath.Join(t.TempDir(), "skill.jsonl")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			check := func(backend chatmodel.Backend, path string) {
				t.Helper()
				turns, err := IndexSessionTurns(backend, path, 0)
				if err != nil || len(turns) != 2 {
					t.Fatalf("skill instructions changed turn ownership: %+v, %v", turns, err)
				}
				for i, want := range []string{request, "Continue"} {
					transcript, err := ReadSessionTurn(backend, path, turns[i])
					if err != nil || len(transcript.Parts) != 2 || transcript.Parts[0].Text != want || transcript.Parts[1].Role != "assistant" {
						t.Fatalf("request/answer lost or skill instructions exposed: %+v, %v", transcript, err)
					}
				}
			}
			check(chatmodel.BackendCodex, path)
			converted, err := ConvertSession(chatmodel.BackendCodex, chatmodel.BackendClaude, path, conversionHomes(t))
			if err != nil {
				t.Fatal(err)
			}
			check(chatmodel.BackendClaude, converted.Path)
		})
	}
	for _, content := range [][]any{
		{map[string]any{"type": "text", "text": request}, map[string]any{"type": "skill", "name": "gh", "path": "/skills/gh/SKILL.md"}},
		{map[string]any{"type": "text", "text": request}, map[string]any{"type": "text", "text": instructions}},
	} {
		parts := codexThreadParts([]any{map[string]any{"type": "userMessage", "content": content}}, 1234)
		if len(parts) != 1 || parts[0].Text != request {
			t.Fatalf("app-server skill input lost its task: %+v", parts)
		}
	}
}

func TestSlashCommandHistoriesKeepCommandsAndContinuationForBothAgents(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			path := filepath.Join("testdata", string(backend)+"-slash-history.jsonl")
			want := []string{"/goal keep testing", "/plan inspect tests", "/review main", "latest release", "$repo:check", "/skill:inspect:repo verify", "Continue checking history"}
			if backend == chatmodel.BackendClaude {
				want = []string{"/goal keep testing", "/plan inspect tests", "/code-review main", "/review main", "/loop 5m inspect tests", "/deep-research inspect history", "/gh latest release", "/repo:check", "Continue checking history"}
			}
			assertSlashHistory(t, backend, path, want)
			root := conversionHomes(t)
			target := chatmodel.BackendClaude
			if backend == target {
				target = chatmodel.BackendCodex
			}
			converted, err := ConvertSession(backend, target, path, root)
			if err != nil {
				t.Fatal(err)
			}
			assertSlashHistory(t, target, converted.Path, want)
			back, err := ConvertSession(target, backend, converted.Path, root)
			if err != nil {
				t.Fatal(err)
			}
			assertSlashHistory(t, backend, back.Path, want)
		})
	}
}

func assertSlashHistory(t *testing.T, backend chatmodel.Backend, path string, want []string) {
	t.Helper()
	turns, err := IndexSessionTurns(backend, path, 0)
	if err != nil || len(turns) != len(want) {
		t.Fatalf("command history disappeared: %d turns, %v", len(turns), err)
	}
	for i, turn := range turns {
		transcript, err := ReadSessionTurn(backend, path, turn)
		if err != nil || len(transcript.Parts) != 2 || transcript.Parts[0].Role != "user" || transcript.Parts[1].Role != "assistant" {
			t.Fatalf("turn %d lost its input/answer or exposed skill instructions: %+v, %v", i, transcript, err)
		}
		prompt, answer := transcript.Parts[0].Text, transcript.Parts[1].Text
		if prompt != want[i] || answer != "Reply to "+want[i] {
			t.Fatalf("turn %d mixed command ownership: %q => %q", i, prompt, answer)
		}
	}
}

func TestClaudeCommandEnvelopeDoesNotExposeLocalOutputOrMetadata(t *testing.T) {
	const valid = "<command-message>gh</command-message>\n<command-name>/gh</command-name>\n<command-args></command-args>"
	for _, text := range []string{
		"<command-name>/clear</command-name>",
		"<command-message>compact</command-message>",
		"<command-message>compact</command-message>\n<command-name>/compact</command-name>",
		"<command-message>compact</command-message>\n<command-name>/compact</command-name>\n<command-args>summarize</command-args>",
		valid + "\n<local-command-stdout>Local output</local-command-stdout>",
		"<command-message>gh</command-message><command-name>/gh</command-name><command-name>/other</command-name>",
		strings.Replace(valid, "/gh", "/gh argument", 1),
	} {
		if command := claudeCommandInput(text); command != "" {
			t.Fatalf("partial/local envelope became a user command: %q", command)
		}
	}
	for _, text := range []string{valid, strings.TrimSuffix(valid, "\n<command-args></command-args>")} {
		if parts := claudeTranscriptParts("user", text, ""); len(parts) != 1 || parts[0].Text != "/gh" {
			t.Fatalf("argumentless skill invocation was lost: %+v", parts)
		}
	}
	for _, flag := range []string{"isMeta", "isSidechain", "isCompactSummary", "isVisibleInTranscriptOnly"} {
		record := map[string]any{"type": "user", flag: true, "message": map[string]any{"content": valid}}
		if parts := claudeSessionParts(record); len(parts) != 0 {
			t.Fatalf("%s exposed internal command metadata: %+v", flag, parts)
		}
	}
}
