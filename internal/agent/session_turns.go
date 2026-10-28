package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// NativeTurn contains only identity and file boundaries, never transcript text.
type NativeTurn struct {
	ID              string
	PromptID        string
	ParentID        string
	PromptIndex     int
	TurnIndex       int
	Ranges          []NativeRange
	Start           int64
	End             int64
	StartedAt       int64
	Model           string
	FinishedAt      int64
	Status          chatmodel.SessionStatus
	ReasoningEffort string
	Error           string
}

type NativeRange struct{ Start, End int64 }

// IndexSessionTurns scans metadata independently of transcript display limits.
// Offsets allow callers to read the newest turns first without replaying older text.
func IndexSessionTurns(backend chatmodel.Backend, path string, offset int64) ([]NativeTurn, error) {
	return IndexSessionTurnsTo(backend, path, offset, 0)
}

// A nonzero end indexes a fixed file snapshot, even if the source keeps growing.
func IndexSessionTurnsTo(backend chatmodel.Backend, path string, offset, end int64) ([]NativeTurn, error) {
	if !backend.Valid() || offset < 0 || (end > 0 && end < offset) {
		return nil, fmt.Errorf("invalid native history request")
	}
	if backend == chatmodel.BackendClaude {
		return indexClaudeTurns(path, offset, end)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	var turns []NativeTurn
	position := offset
	model, effort := "", ""
	lastStamp := int64(0)
	codexExplicit := false
	reader := nativeReader(file, offset, end)
	for {
		line, readErr := reader.ReadBytes('\n')
		start := position
		position += int64(len(line))
		var record map[string]any
		if len(line) <= maxEventBytes && json.Unmarshal(line, &record) == nil {
			stamp := nativeTimestamp(record["timestamp"])
			typ := stringValue(record["type"])
			payload, _ := record["payload"].(map[string]any)
			id, prompt := "", ""
			boundary := false
			if backend == chatmodel.BackendCodex {
				if typ == "turn_context" {
					model = stringValue(payload["model"])
					effort = stringValue(payload["effort"])
					if effort == "" {
						effort = stringValue(payload["reasoning_effort"])
					}
					id = stringValue(payload["turn_id"])
					boundary = id != ""
					codexExplicit = codexExplicit || boundary
				} else if typ == "event_msg" && payload["type"] == "task_started" {
					id = stringValue(payload["turn_id"])
					boundary = id != ""
					codexExplicit = codexExplicit || boundary
				} else if typ == "response_item" && payload["type"] == "message" && payload["role"] == "user" && genuineUserContent(payload["content"]) {
					id = stringValue(payload["id"])
					// A task_started/turn_context boundary already owns this input.
					boundary = !codexExplicit
				}
			}
			if boundary && len(turns) > 0 && id != "" && id == turns[len(turns)-1].ID {
				boundary = false
			}
			if boundary {
				if len(turns) > 0 {
					previous := &turns[len(turns)-1]
					previous.End = start
					if previous.FinishedAt == 0 {
						previous.FinishedAt, previous.Status = lastStamp, chatmodel.StatusCompleted
					}
				}
				turns = append(turns, NativeTurn{ID: id, PromptID: prompt, Start: start, StartedAt: stamp, Model: model, ReasoningEffort: effort, Status: chatmodel.StatusRunning})
			}
			if len(turns) > 0 && model != "" && typ == "turn_context" {
				turns[len(turns)-1].Model, turns[len(turns)-1].ReasoningEffort = model, effort
			}
			if len(turns) > 0 && typ == "event_msg" {
				turn := &turns[len(turns)-1]
				if eventID := stringValue(payload["turn_id"]); eventID == "" || eventID == turn.ID {
					switch stringValue(payload["type"]) {
					case "task_complete":
						turn.Status, turn.FinishedAt = chatmodel.StatusCompleted, stamp
					case "turn_aborted":
						turn.Status, turn.FinishedAt = chatmodel.StatusStopped, stamp
					case "task_failed":
						turn.Status, turn.FinishedAt, turn.Error = chatmodel.StatusFailed, stamp, stringValue(payload["error"])
					}
				}
			}
			if stamp > 0 {
				lastStamp = stamp
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, readErr
			}
			break
		}
	}
	if len(turns) > 0 {
		turns[len(turns)-1].End = position
		if !codexExplicit && turns[len(turns)-1].FinishedAt == 0 {
			turns[len(turns)-1].FinishedAt, turns[len(turns)-1].Status = lastStamp, chatmodel.StatusCompleted
		}
	} else if position > offset {
		// Old or output-only native files have no explicit input boundary.
		turns = append(turns, NativeTurn{Start: offset, End: position, Status: chatmodel.StatusCompleted})
	}
	return turns, nil
}

func genuineUserContent(content any) bool {
	if text, ok := content.(string); ok {
		return genuineUserText(text)
	}
	blocks, _ := content.([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		switch stringValue(block["type"]) {
		case "image", "input_image":
			return true
		case "text", "input_text":
			if genuineUserText(stringValue(block["text"])) {
				return true
			}
		}
	}
	return false
}

func genuineUserText(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && !strings.HasPrefix(text, "<environment_context>") && !strings.HasPrefix(text, "<permissions instructions>") && !strings.HasPrefix(text, "<system-reminder>") && !strings.HasPrefix(text, "# AGENTS.md") && !strings.HasPrefix(text, "[Request interrupted") && !strings.HasPrefix(text, "This session is being continued from a previous conversation")
}

func ReadSessionTurn(backend chatmodel.Backend, path string, turn NativeTurn) (chatmodel.Transcript, error) {
	if !backend.Valid() || turn.Start < 0 || turn.End < turn.Start {
		return chatmodel.Transcript{}, fmt.Errorf("invalid native turn boundary")
	}
	file, err := os.Open(path)
	if err != nil {
		return chatmodel.Transcript{}, err
	}
	defer file.Close()
	if len(turn.Ranges) > 0 {
		readers := make([]io.Reader, 0, len(turn.Ranges))
		for _, span := range turn.Ranges {
			if span.Start < turn.Start || span.End < span.Start || span.End > turn.End {
				return chatmodel.Transcript{}, fmt.Errorf("invalid native record boundary")
			}
			readers = append(readers, io.NewSectionReader(file, span.Start, span.End-span.Start))
		}
		return parseTranscriptReader(backend, io.MultiReader(readers...), turn.Model, true)
	}
	return parseTranscriptReader(backend, io.NewSectionReader(file, turn.Start, turn.End-turn.Start), turn.Model, true)
}

func indexClaudeTurns(path string, offset, end int64) ([]NativeTurn, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	var turns []NativeTurn
	byPrompt, byUUID := map[string]int{}, map[string]int{}
	reader := nativeReader(file, offset, end)
	position := offset
	current := -1
	for {
		line, readErr := reader.ReadBytes('\n')
		start := position
		position += int64(len(line))
		var record map[string]any
		if len(line) <= maxEventBytes && json.Unmarshal(line, &record) == nil && record["isSidechain"] != true {
			id, parent, prompt := stringValue(record["uuid"]), stringValue(record["parentUuid"]), stringValue(record["promptId"])
			message, _ := record["message"].(map[string]any)
			owner := current
			if previous, ok := byUUID[parent]; ok {
				owner = previous
			}
			if previous, ok := byPrompt[prompt]; ok && prompt != "" {
				owner = previous
			}
			input := record["type"] == "user" && record["isMeta"] != true && record["isCompactSummary"] != true && record["isVisibleInTranscriptOnly"] != true && genuineUserContent(message["content"])
			_, known := byPrompt[prompt]
			if input && (prompt == "" || !known) {
				if current >= 0 && turns[current].Status == chatmodel.StatusRunning {
					turns[current].Status = chatmodel.StatusCompleted
				}
				turn := NativeTurn{ID: id, PromptID: prompt, ParentID: parent, Start: start, StartedAt: nativeTimestamp(record["timestamp"]), Status: chatmodel.StatusRunning}
				if pos, ok := record["turnPosition"].(map[string]any); ok {
					turn.PromptIndex, _ = numberValue(pos["promptIndex"])
					turn.TurnIndex, _ = numberValue(pos["turnIndex"])
				}
				turns = append(turns, turn)
				current, owner = len(turns)-1, len(turns)-1
				if prompt != "" {
					byPrompt[prompt] = owner
				}
			}
			if owner >= 0 {
				if id != "" {
					byUUID[id] = owner
				}
				turn := &turns[owner]
				if model := stringValue(message["model"]); model != "" && model != "<synthetic>" {
					turn.Model = model
				}
				if effort := stringValue(record["perTurnEffort"]); effort != "" {
					turn.ReasoningEffort = effort
				}
				meaningful := record["type"] == "user" || record["type"] == "assistant" || record["subtype"] == "turn_duration"
				if stamp := nativeTimestamp(record["timestamp"]); stamp > 0 && meaningful {
					turn.FinishedAt = stamp
				}
				if turn.Status != chatmodel.StatusStopped {
					switch stringValue(message["stop_reason"]) {
					case "end_turn", "stop_sequence", "max_tokens":
						turn.Status = chatmodel.StatusCompleted
					case "":
						// Older and converted sessions omit stop_reason. Tool-use
						// and thinking-only records are not a completed answer.
						if record["type"] == "assistant" && claudeFinalText(message["content"]) {
							turn.Status = chatmodel.StatusCompleted
						}
					}
					if record["subtype"] == "turn_duration" {
						turn.Status = chatmodel.StatusCompleted
					}
				}
				if record["type"] == "user" && strings.HasPrefix(strings.TrimSpace(claudeUserText(message["content"])), "[Request interrupted") {
					turn.Status = chatmodel.StatusStopped
				}
				n := len(turn.Ranges)
				if n > 0 && turn.Ranges[n-1].End == start {
					turn.Ranges[n-1].End = position
				} else {
					turn.Ranges = append(turn.Ranges, NativeRange{start, position})
				}
				turn.End = position
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, readErr
			}
			break
		}
	}
	if len(turns) == 0 && position > offset {
		turns = append(turns, NativeTurn{Start: offset, End: position, Status: chatmodel.StatusCompleted})
	}
	return turns, nil
}

func claudeFinalText(content any) bool {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	blocks, _ := content.([]any)
	text := false
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block["type"] == "tool_use" {
			return false
		}
		text = text || (block["type"] == "text" && strings.TrimSpace(stringValue(block["text"])) != "")
	}
	return text
}

func nativeTimestamp(value any) int64 {
	stamp, _ := time.Parse(time.RFC3339Nano, stringValue(value))
	if stamp.IsZero() {
		return 0
	}
	return stamp.UnixMilli()
}

func nativeReader(file *os.File, offset, end int64) *bufio.Reader {
	var reader io.Reader = file
	if end > 0 {
		reader = io.LimitReader(file, end-offset)
	}
	return bufio.NewReader(reader)
}

func claudeUserText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, _ := content.([]any)
	for _, raw := range blocks {
		if b, ok := raw.(map[string]any); ok && b["type"] == "text" {
			return stringValue(b["text"])
		}
	}
	return ""
}
