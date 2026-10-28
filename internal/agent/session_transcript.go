package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	chatmodel "spool/internal/model"
)

// ParseSessionTranscript reads a native Codex rollout or Claude Code JSONL
// session and returns Magpie-compatible transcript parts. The native files are
// treated as read-only; malformed or provider metadata records are ignored so
// a partially-written session can still be displayed.
func ParseSessionTranscript(backend chatmodel.Backend, path string) (chatmodel.Transcript, error) {
	return ParseSessionTranscriptFrom(backend, path, 0)
}

// ParseSessionTranscriptFrom reads native records starting at a byte offset.
func ParseSessionTranscriptFrom(backend chatmodel.Backend, path string, offset int64) (chatmodel.Transcript, error) {
	transcript := chatmodel.Transcript{Parts: []chatmodel.Part{}}
	if path == "" {
		return transcript, errors.New("session transcript path is empty")
	}
	if !backend.Valid() {
		return transcript, fmt.Errorf("unsupported session backend %q", backend)
	}
	file, err := os.Open(path)
	if err != nil {
		return transcript, err
	}
	defer file.Close()
	if offset < 0 {
		return transcript, errors.New("invalid native transcript offset")
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return transcript, err
	}

	carryNotes := backend == chatmodel.BackendCodex && offset > 0 && codexCarryAt(file, offset)
	return parseTranscriptReader(backend, file, "", carryNotes)
}

func parseTranscriptReader(backend chatmodel.Backend, reader io.Reader, model string, carryNotes bool) (chatmodel.Transcript, error) {
	transcript := chatmodel.Transcript{Parts: []chatmodel.Part{}}
	codex := newCodexHistory()
	err := readJSONLines(reader, func(line []byte) error {
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			return nil
		}
		var parts []chatmodel.Part
		switch backend {
		case chatmodel.BackendClaude:
			parts = claudeSessionParts(record)
		case chatmodel.BackendCodex:
			carryNotes = codexCarryNotes(record, carryNotes)
			if stringValue(record["type"]) == "turn_context" {
				payload, _ := record["payload"].(map[string]any)
				model = stringValue(payload["model"])
				return nil
			}
			switch stringValue(record["type"]) {
			case "response_item":
				payload, _ := record["payload"].(map[string]any)
				parts = codex.addResponse(payload, model, &transcript)
			case "event_msg":
				payload, _ := record["payload"].(map[string]any)
				parts = codex.addEvent(payload, model, &transcript)
			}
		default:
			return fmt.Errorf("unsupported session backend %q", backend)
		}
		if backend == chatmodel.BackendCodex && carryNotes {
			parts = restoreCarryNotes(parts)
		}
		for _, part := range parts {
			chatmodel.AppendPart(&transcript, part)
		}
		return nil
	})
	if err != nil {
		return transcript, err
	}
	codex.finish(&transcript)
	return transcript, nil
}

// codexCallRef lets event_msg.item_completed records enrich the response_item
// records around them. Codex writes a tool call first, then an event containing
// the native command/file result, and finally a response_item tool result. The
// IDs on those records are not always the same, so we also keep an ordered
// queue for command execution events.
type codexCallRef struct {
	callID      string
	useIndex    int
	resultIndex int
	eventKind   string
	eventOutput string
}

type codexHistory struct {
	calls                []*codexCallRef
	byCallID             map[string]*codexCallRef
	pendingAgentMessages map[string]int
	eventCursor          int
}

func newCodexHistory() *codexHistory {
	return &codexHistory{
		byCallID:             make(map[string]*codexCallRef),
		pendingAgentMessages: make(map[string]int),
	}
}

func (h *codexHistory) addResponse(item map[string]any, model string, transcript *chatmodel.Transcript) []chatmodel.Part {
	parts := codexTranscriptParts(item, model)
	kind := codexItemKind(item)
	if kind == "message" {
		if stringValue(item["role"]) != "assistant" {
			// An event-only assistant message without a matching response item
			// must not suppress an identically-worded answer in a later turn.
			h.pendingAgentMessages = make(map[string]int)
		}
		filtered := parts[:0]
		for _, part := range parts {
			if part.Role == "assistant" && part.Kind == "text" && h.pendingAgentMessages[part.Text] > 0 {
				h.pendingAgentMessages[part.Text]--
				continue
			}
			filtered = append(filtered, part)
		}
		parts = filtered
	}
	if kind == "reasoning" {
		filtered := parts[:0]
		for _, part := range parts {
			duplicate := false
			for _, existing := range transcript.Parts {
				if existing.Kind == "thinking" && existing.Text == part.Text {
					duplicate = true
					break
				}
			}
			if !duplicate {
				filtered = append(filtered, part)
			}
		}
		parts = filtered
	}
	if kind == "functioncall" || kind == "customtoolcall" || kind == "localshellcall" {
		call := &codexCallRef{callID: stringValue(item["call_id"]), useIndex: len(transcript.Parts), resultIndex: -1}
		h.calls = append(h.calls, call)
		if call.callID != "" {
			h.byCallID[call.callID] = call
		}
	} else if kind == "functioncalloutput" || kind == "customtoolcalloutput" {
		call := h.byCallID[stringValue(item["call_id"])]
		if call != nil {
			if call.eventKind == "filechange" && call.eventOutput != "" {
				for i := range parts {
					if parts[i].Kind == "tool_result" {
						parts[i].Text = call.eventOutput
					}
				}
			} else if call.eventOutput != "" {
				for i := range parts {
					if parts[i].Kind == "tool_result" && strings.TrimSpace(parts[i].Text) == "" {
						parts[i].Text = call.eventOutput
					}
				}
			}
			for _, part := range parts {
				if part.Kind == "tool_result" && strings.TrimSpace(part.Text) != "" {
					call.resultIndex = len(transcript.Parts)
					break
				}
			}
		}
	}
	return parts
}

func (h *codexHistory) addEvent(payload map[string]any, model string, transcript *chatmodel.Transcript) []chatmodel.Part {
	if stringValue(payload["type"]) != "item_completed" {
		return nil
	}
	item, _ := payload["item"].(map[string]any)
	if item == nil {
		return nil
	}
	switch kind := codexItemKind(item); kind {
	case "reasoning":
		// response_item reasoning can contain only encrypted content. The event
		// record is the readable summary used by the native Codex UI.
		if summary := codexReasoningSummary(item); summary != "" {
			for _, existing := range transcript.Parts {
				if existing.Kind == "thinking" && existing.Text == summary {
					return nil
				}
			}
			return []chatmodel.Part{{Role: "assistant", Kind: "thinking", Text: summary, Model: model}}
		}
	case "commandexecution":
		return h.addCommandExecution(item, model, transcript)
	case "filechange":
		return h.addFileChange(item, model, transcript)
	case "agentmessage":
		// Some native Codex rollouts only persist the human-readable final
		// answer in event_msg.item_completed.AgentMessage. Newer rollouts also
		// write a response_item.message for the same text, so suppress exact
		// duplicates when both records are present.
		text := codexAgentMessageText(item)
		if text == "" {
			return nil
		}
		for _, existing := range transcript.Parts {
			if existing.Role == "assistant" && existing.Kind == "text" && existing.Text == text {
				return nil
			}
		}
		h.pendingAgentMessages[text]++
		return []chatmodel.Part{{Role: "assistant", Kind: "text", Text: text, Model: model}}
	}
	return nil
}

func codexAgentMessageText(item map[string]any) string {
	content := item["content"]
	if text := stringValue(content); text != "" {
		return text
	}
	blocks, _ := content.([]any)
	var text []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block == nil {
			continue
		}
		kind := strings.ToLower(stringValue(block["type"]))
		if kind != "" && kind != "text" && kind != "output_text" {
			continue
		}
		if value := stringValue(block["text"]); value != "" {
			text = append(text, value)
		}
	}
	return strings.Join(text, "\n\n")
}

func (h *codexHistory) addCommandExecution(item map[string]any, model string, transcript *chatmodel.Transcript) []chatmodel.Part {
	command := codexCommandText(item)
	var call *codexCallRef
	// CommandExecution IDs are different from response_item call IDs. Match in
	// file order, preferring an un-enriched shell call immediately before this
	// event.
	for i := len(h.calls) - 1; i >= h.eventCursor; i-- {
		candidate := h.calls[i]
		if candidate.eventKind == "" && candidate.useIndex >= 0 {
			call = candidate
			h.eventCursor = i + 1
			break
		}
	}
	if call == nil {
		for i := len(h.calls) - 1; i >= 0; i-- {
			if h.calls[i].eventKind == "" && h.calls[i].useIndex >= 0 {
				call = h.calls[i]
				break
			}
		}
	}
	output := codexCommandOutput(item)
	if call != nil {
		call.eventKind, call.eventOutput = "commandexecution", output
		if command != "" && call.useIndex >= 0 && call.useIndex < len(transcript.Parts) {
			transcript.Parts[call.useIndex].Text = command
		}
		if call.resultIndex >= 0 && call.resultIndex < len(transcript.Parts) && output != "" {
			transcript.Parts[call.resultIndex].Text = output
		}
		return nil
	}
	if command == "" {
		command = codexActivity(item)
	}
	parts := []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: "shell", Text: command, Model: model}}
	if output != "" {
		parts = append(parts, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: output})
	}
	return parts
}

func (h *codexHistory) addFileChange(item map[string]any, model string, transcript *chatmodel.Transcript) []chatmodel.Part {
	callID := stringValue(item["id"])
	call := h.byCallID[callID]
	diff := codexFileChangeDiff(item)
	if call != nil {
		call.eventKind, call.eventOutput = "filechange", diff
		if call.resultIndex >= 0 && call.resultIndex < len(transcript.Parts) && diff != "" {
			transcript.Parts[call.resultIndex].Text = diff
		}
		return nil
	}
	parts := []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: "fileChange", Text: "File change", Model: model}}
	if diff != "" {
		parts = append(parts, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: diff})
	}
	return parts
}

func (h *codexHistory) finish(transcript *chatmodel.Transcript) {
	// A native event can be the only record carrying a result. Insert it after
	// its tool use when no response_item output was written later.
	for i := len(h.calls) - 1; i >= 0; i-- {
		call := h.calls[i]
		if call.resultIndex >= 0 || call.eventOutput == "" || call.useIndex < 0 || call.useIndex >= len(transcript.Parts) {
			continue
		}
		part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: call.eventOutput}
		at := call.useIndex + 1
		transcript.Parts = append(transcript.Parts, chatmodel.Part{})
		copy(transcript.Parts[at+1:], transcript.Parts[at:])
		transcript.Parts[at] = part
		for _, ref := range h.calls {
			if ref.useIndex >= at {
				ref.useIndex++
			}
			if ref.resultIndex >= at {
				ref.resultIndex++
			}
		}
	}
}

func codexReasoningSummary(item map[string]any) string {
	summary, _ := item["summary_text"].([]any)
	if len(summary) == 0 {
		summary, _ = item["summary"].([]any)
	}
	var text []string
	for _, value := range summary {
		if block, ok := value.(map[string]any); ok {
			if value := stringValue(block["text"]); value != "" {
				text = append(text, value)
			}
		} else if value := stringValue(value); value != "" {
			text = append(text, value)
		}
	}
	return strings.Join(text, "\n\n")
}

func claudeSessionParts(record map[string]any) []chatmodel.Part {
	typ := stringValue(record["type"])
	if typ != "user" && typ != "assistant" {
		return nil
	}
	if record["isSidechain"] == true || (typ == "user" && (record["isMeta"] == true || record["isCompactSummary"] == true || record["isVisibleInTranscriptOnly"] == true)) {
		return nil
	}
	message, _ := record["message"].(map[string]any)
	role := stringValue(message["role"])
	if role == "" {
		role = typ
	}
	model := stringValue(message["model"])
	carryNotes := model == "<synthetic>"
	if carryNotes {
		model = ""
	}
	parts := claudeTranscriptParts(role, message["content"], model)
	if typ == "user" {
		filtered := parts[:0]
		for _, part := range parts {
			if part.Kind == "text" && !genuineUserText(part.Text) {
				continue
			}
			filtered = append(filtered, part)
		}
		parts = filtered
	}
	if typ == "user" {
		if diff := claudeToolResultDiff(record["toolUseResult"]); diff != "" {
			for i := range parts {
				if parts[i].Kind == "tool_result" {
					parts[i].Text = strings.TrimSpace(parts[i].Text + "\n" + diff)
					break
				}
			}
		}
	}
	if carryNotes {
		parts = restoreCarryNotes(parts)
	}
	return parts
}

// claudeToolResultDiff preserves Claude's structured file changes in the
// shared transcript so the web client can render them with a diff viewer.
func claudeToolResultDiff(value any) string {
	result, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	path := stringValue(result["filePath"])
	if path == "" {
		path = "file"
	}
	var body []string
	if entries, ok := result["structuredPatch"].([]any); ok {
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			oldStart, oldOK := numberValue(entry["oldStart"])
			oldLines, oldLinesOK := numberValue(entry["oldLines"])
			newStart, newOK := numberValue(entry["newStart"])
			newLines, newLinesOK := numberValue(entry["newLines"])
			if !oldOK {
				oldStart = 0
			}
			if !oldLinesOK {
				oldLines = 0
			}
			if !newOK {
				newStart = 0
			}
			if !newLinesOK {
				newLines = 0
			}
			body = append(body, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldLines, newStart, newLines))
			lines, _ := entry["lines"].([]any)
			for _, rawLine := range lines {
				line := stringValue(rawLine)
				if line == "" || (line[0] != ' ' && line[0] != '+' && line[0] != '-') {
					line = " " + line
				}
				body = append(body, line)
			}
		}
	}
	if len(body) == 0 {
		kind := stringValue(result["type"])
		content := stringValue(result["content"])
		if content == "" {
			content = stringValue(result["newString"])
		}
		if kind == "delete" {
			content = stringValue(result["originalFile"])
			if content != "" {
				lines := prefixedLines(content, "-")
				body = append([]string{fmt.Sprintf("@@ -1,%d +0,0 @@", len(lines))}, lines...)
			}
		} else if kind == "create" && content != "" {
			lines := prefixedLines(content, "+")
			body = append([]string{fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines))}, lines...)
		}
	}
	if len(body) == 0 {
		return ""
	}
	return strings.Join(append([]string{
		"diff --git a/" + path + " b/" + path,
		"--- a/" + path,
		"+++ b/" + path,
	}, body...), "\n")
}

func prefixedLines(text, prefix string) []string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, prefix+line)
	}
	return out
}

func numberValue(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		return int(number), true
	case int:
		return number, true
	case json.Number:
		parsed, err := strconv.Atoi(string(number))
		return parsed, err == nil
	default:
		return 0, false
	}
}
