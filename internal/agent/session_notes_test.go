package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCarryNotesRequireCompleteCanonicalFormat(t *testing.T) {
	for _, note := range []carryItem{
		{Name: "apply_patch", Input: "*** Begin Patch\n*** Add File: pong.html\n+<html>\n*** End Patch", Result: "Success. Updated the following files:\nA pong.html", Done: true},
		{Name: "Read", Input: `{"file_path":"pong.html"}`},
		{Name: "Edit", Input: "contains ``` and\n[result]\ninside the input", Result: "contains ````\n[error]\ninside the output", Done: true, IsError: true},
		{Name: "mcp.tool", Result: "result without input", Done: true},
		{Name: "Write", Input: "data", Done: true},
		{Name: "apply_patch", Input: strings.Repeat("+complete patch line\n", 60_000), Result: strings.Repeat("complete output\n", 60_000), Done: true},
	} {
		got, ok := parseCarryNote(note.note())
		if !ok || got.Name != note.Name || got.Input != strings.TrimRight(note.Input, "\n") || got.Result != strings.TrimRight(note.Result, "\n") || got.Done != note.Done || got.IsError != note.IsError {
			t.Fatalf("failed to restore %s note: ok=%v input=%d/%d result=%d/%d", note.Name, ok, len(got.Input), len(note.Input), len(got.Result), len(note.Result))
		}
	}
	for _, text := range []string{
		"[apply_patch]", "[apply_patch]\n", "[]\n```\ndata\n```\n", "[a tool]\n```\ndata\n```\n",
		"Use [apply_patch] to edit a file", "[apply_patch]\n```diff\n+example\n```\n",
		"[apply_patch]\n```\nunclosed", "[apply_patch]\n```\nexample\n```\nMore explanation",
		"Here is an example:\n[apply_patch]\n```\nexample\n```\n",
	} {
		if _, ok := parseCarryNote(text); ok {
			t.Fatalf("ordinary or incomplete assistant text became a tool: %q", text)
		}
	}
}

func TestConvertedHistoryRestoresToolsInFullAndTurnReads(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			root := conversionHomes(t)
			patch := "*** Begin Patch\n*** Add File: pong.html\n+<html>complete patch</html>\n*** End Patch"
			output := "Success. Updated the following files:\nA pong.html"
			items := []carryItem{
				{Kind: "user", Text: "add a pong game"},
				{Kind: "call", Name: "apply_patch", Input: patch, Result: output, Done: true},
				{Kind: "assistant", Text: "Added the game. [apply_patch] is a tool name."},
				{Kind: "user", Text: "check the game"},
				{Kind: "call", Name: "Read", Input: `{"file_path":"missing.html"}`, Result: "file not found", Done: true, IsError: true},
				{Kind: "assistant", Text: "The file is missing."},
			}
			write := writeCodexCarry
			if backend == chatmodel.BackendClaude {
				write = writeClaudeCarry
			}
			converted, err := write(root, root, items, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(converted.Path)
			if err != nil {
				t.Fatal(err)
			}
			want := []chatmodel.Part{
				{Role: "user", Kind: "text", Text: items[0].Text},
				{Role: "assistant", Kind: "tool_use", Name: "apply_patch", Text: patch},
				{Role: "tool", Kind: "tool_result", Text: output},
				{Role: "assistant", Kind: "text", Text: items[2].Text},
				{Role: "user", Kind: "text", Text: items[3].Text},
				{Role: "assistant", Kind: "tool_use", Name: "Read", Text: items[4].Input},
				{Role: "tool", Kind: "tool_result", Text: "[error]\nfile not found"},
				{Role: "assistant", Kind: "text", Text: items[5].Text},
			}
			full, err := ParseSessionTranscript(backend, converted.Path)
			if err != nil || !reflect.DeepEqual(full.Parts, want) {
				t.Fatalf("full history misplaced a converted tool: %+v, %v", full.Parts, err)
			}
			turns, err := IndexSessionTurns(backend, converted.Path, 0)
			if err != nil || len(turns) != 2 {
				t.Fatalf("wrong converted turn boundaries: %+v, %v", turns, err)
			}
			var combined []chatmodel.Part
			for _, turn := range turns {
				transcript, err := ReadSessionTurn(backend, converted.Path, turn)
				if err != nil {
					t.Fatal(err)
				}
				combined = append(combined, transcript.Parts...)
			}
			if !reflect.DeepEqual(combined, want) {
				t.Fatalf("per-turn history misplaced a converted tool: %+v", combined)
			}
			later, err := ParseSessionTranscriptFrom(backend, converted.Path, turns[1].Start)
			if err != nil || !reflect.DeepEqual(later.Parts, want[4:]) {
				t.Fatalf("offset read lost conversion provenance: %+v, %v", later.Parts, err)
			}
			laterTurns, err := IndexSessionTurns(backend, converted.Path, turns[1].Start)
			if err != nil || len(laterTurns) != 1 {
				t.Fatalf("offset index failed: %+v, %v", laterTurns, err)
			}
			later, err = ReadSessionTurn(backend, converted.Path, laterTurns[0])
			if err != nil || !reflect.DeepEqual(later.Parts, want[4:]) {
				t.Fatalf("offset index lost conversion provenance: %+v, %v", later.Parts, err)
			}
			after, _ := os.ReadFile(converted.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("display normalization changed the native context")
			}
		})
	}
}

func TestCarryNotesDoNotReclassifyOrdinaryNativeOrUserMessages(t *testing.T) {
	note := (carryItem{Name: "apply_patch", Input: "example patch", Result: "example result", Done: true}).note()
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			var body bytes.Buffer
			encoder := json.NewEncoder(&body)
			for _, role := range []string{"user", "assistant"} {
				record := map[string]any{"type": role, "message": map[string]any{"role": role, "model": "real-model", "content": note}}
				if backend == chatmodel.BackendCodex {
					record = map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": "text", "text": note}}}}
				}
				if err := encoder.Encode(record); err != nil {
					t.Fatal(err)
				}
			}
			for _, converted := range []bool{false, true} {
				transcript, err := parseTranscriptReader(backend, strings.NewReader(body.String()), "", converted)
				if err != nil || len(transcript.Parts) < 2 || transcript.Parts[0].Kind != "text" || transcript.Parts[0].Text != note {
					t.Fatalf("user message was reclassified: %+v, %v", transcript.Parts, err)
				}
				if (!converted || backend == chatmodel.BackendClaude) && (len(transcript.Parts) != 2 || transcript.Parts[1].Kind != "text" || transcript.Parts[1].Text != note) {
					t.Fatalf("ordinary native answer was reclassified: %+v", transcript.Parts)
				}
			}
		})
	}
}

func TestToolActivitiesSurviveNativeConversionRoundTrip(t *testing.T) {
	root := conversionHomes(t)
	path := filepath.Join(root, "original.jsonl")
	patch := "*** Begin Patch\n*** Add File: pong.html\n+complete game\n*** End Patch"
	lines := [][]byte{
		jsonLine(o("type", "response_item", "payload", o("type", "message", "role", "user", "content", []any{o("type", "input_text", "text", "add a game")}))),
		jsonLine(o("type", "response_item", "payload", o("type", "custom_tool_call", "name", "apply_patch", "call_id", "patch", "input", patch))),
		jsonLine(o("type", "response_item", "payload", o("type", "custom_tool_call_output", "call_id", "patch", "output", "Success"))),
		jsonLine(o("type", "response_item", "payload", o("type", "message", "role", "assistant", "content", []any{o("type", "output_text", "text", "Done")}))),
	}
	if err := writeNew(path, lines); err != nil {
		t.Fatal(err)
	}
	from := chatmodel.BackendCodex
	for _, to := range []chatmodel.Backend{chatmodel.BackendClaude, chatmodel.BackendCodex, chatmodel.BackendClaude} {
		converted, err := ConvertSession(from, to, path, root)
		if err != nil {
			t.Fatal(err)
		}
		transcript, err := ParseSessionTranscript(to, converted.Path)
		if err != nil || len(transcript.Parts) != 4 || transcript.Parts[1].Kind != "tool_use" || transcript.Parts[1].Name != "apply_patch" || transcript.Parts[1].Text != patch || transcript.Parts[2].Kind != "tool_result" || transcript.Parts[2].Text != "Success" || transcript.Parts[3].Text != "Done" {
			t.Fatalf("%s to %s conversion exposed tool details as prose: %+v, %v", from, to, transcript.Parts, err)
		}
		from, path = to, converted.Path
	}
}

func TestResumedCodexDoesNotTreatNewAnswersAsImportedTools(t *testing.T) {
	root := t.TempDir()
	note := (carryItem{Name: "apply_patch", Input: "example patch", Result: "example result", Done: true}).note()
	converted, err := writeCodexCarry(root, root, []carryItem{
		{Kind: "user", Text: "old question"},
		{Kind: "assistant", Text: "old answer"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(converted.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, record := range []ord{
		o("type", "event_msg", "payload", o("type", "task_started", "turn_id", "new-turn")),
		o("type", "turn_context", "payload", o("turn_id", "new-turn", "model", "real-model")),
		o("type", "response_item", "payload", o("type", "message", "role", "user", "content", []any{o("type", "input_text", "text", "show a tool note example")})),
		o("type", "response_item", "payload", o("type", "message", "role", "assistant", "content", []any{o("type", "output_text", "text", note)})),
	} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(converted.Path)
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(bytes.LastIndex(data, []byte(`{"type":"response_item"`)))
	offsetRead, err := ParseSessionTranscriptFrom(chatmodel.BackendCodex, converted.Path, offset)
	if err != nil || len(offsetRead.Parts) != 1 || offsetRead.Parts[0].Kind != "text" || offsetRead.Parts[0].Text != note {
		t.Fatalf("offset read reclassified a resumed answer: %+v, %v", offsetRead.Parts, err)
	}
	want := chatmodel.Part{Role: "assistant", Kind: "text", Text: note, Model: "real-model"}
	full, err := ParseSessionTranscript(chatmodel.BackendCodex, converted.Path)
	if err != nil || len(full.Parts) != 4 || full.Parts[3] != want {
		t.Fatalf("new assistant answer became an imported tool: %+v, %v", full.Parts, err)
	}
	turns, err := IndexSessionTurns(chatmodel.BackendCodex, converted.Path, 0)
	if err != nil || len(turns) != 2 || turns[1].CarryNotes {
		t.Fatalf("resumed turn kept imported provenance: %+v, %v", turns, err)
	}
	last, err := ReadSessionTurn(chatmodel.BackendCodex, converted.Path, turns[1])
	if err != nil || len(last.Parts) != 2 || last.Parts[1] != want {
		t.Fatalf("resumed turn answer became an imported tool: %+v, %v", last.Parts, err)
	}
}
