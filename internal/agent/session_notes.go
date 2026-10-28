package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	chatmodel "spool/internal/model"
)

// Non-shell calls remain assistant notes in the target CLI's context because
// that provider may not have the original tool. Only their display is restored.
func restoreCarryNotes(parts []chatmodel.Part) []chatmodel.Part {
	var restored []chatmodel.Part
	for i, part := range parts {
		if part.Role == "assistant" && part.Kind == "text" {
			if note, ok := parseCarryNote(part.Text); ok {
				if restored == nil {
					restored = append(make([]chatmodel.Part, 0, len(parts)+1), parts[:i]...)
				}
				input := note.Input
				if input == "" {
					input = note.Name
				}
				restored = append(restored, chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: note.Name, Text: input, Model: part.Model})
				if note.Done {
					result := note.Result
					if note.IsError {
						result = "[error]\n" + result
					}
					restored = append(restored, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: result})
				}
				continue
			}
		}
		if restored != nil {
			restored = append(restored, part)
		}
	}
	if restored != nil {
		return restored
	}
	return parts
}

// Require the converter's entire canonical format, not just a [tool] heading.
func parseCarryNote(text string) (carryItem, bool) {
	note := carryItem{Kind: "call"}
	heading, rest, ok := strings.Cut(text, "\n")
	if !ok || !strings.HasPrefix(heading, "[") || !strings.HasSuffix(heading, "]") {
		return note, false
	}
	note.Name = heading[1 : len(heading)-1]
	if note.Name == "" || strings.ContainsAny(note.Name, "[] \t\r\n") {
		return note, false
	}
	if strings.HasPrefix(rest, "```") {
		note.Input, rest, ok = readCarryFence(rest)
		if !ok {
			return note, false
		}
	}
	if strings.HasPrefix(rest, "\n[result]\n") || strings.HasPrefix(rest, "\n[error]\n") {
		note.IsError = strings.HasPrefix(rest, "\n[error]\n")
		_, rest, _ = strings.Cut(rest[1:], "\n")
		note.Result, rest, ok = readCarryFence(rest)
		if !ok {
			return note, false
		}
		note.Done = true
	}
	return note, rest == "" && (note.Input != "" || note.Done) && note.note() == text
}

func readCarryFence(text string) (body, rest string, ok bool) {
	marker, rest, ok := strings.Cut(text, "\n")
	if !ok || len(marker) < 3 || strings.Trim(marker, "`") != "" {
		return "", text, false
	}
	// fence() always writes a newline before and after the closing marker.
	end := strings.Index(rest, "\n"+marker+"\n")
	if end < 0 {
		return "", text, false
	}
	return rest[:end], rest[end+len(marker)+2:], true
}

func codexCarryMeta(record map[string]any) bool {
	if record["type"] != "session_meta" {
		return false
	}
	payload, _ := record["payload"].(map[string]any)
	// These are the synthetic metadata fields written by writeCodexCarry.
	return payload["cli_version"] == "0.0.0" && payload["originator"] == "codex_cli_rs" && payload["source"] == "cli"
}

func codexCarryNotes(record map[string]any, imported bool) bool {
	switch record["type"] {
	case "session_meta":
		return codexCarryMeta(record)
	case "turn_context":
		return false
	case "event_msg":
		payload, _ := record["payload"].(map[string]any)
		if payload["type"] == "task_started" {
			return false
		}
	}
	return imported
}

// Offset readers may start inside imported notes or after the first resumed
// turn. Inspect that prefix without changing the caller's file position.
func codexCarryAt(file *os.File, offset int64) bool {
	reader := bufio.NewReader(io.NewSectionReader(file, 0, offset))
	line, err := reader.ReadBytes('\n')
	var record map[string]any
	if json.Unmarshal(line, &record) != nil || !codexCarryMeta(record) {
		return false
	}
	for err == nil {
		line, err = reader.ReadBytes('\n')
		record = nil
		if json.Unmarshal(line, &record) == nil && !codexCarryNotes(record, true) {
			return false
		}
	}
	return true
}
