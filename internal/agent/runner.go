package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	chatmodel "spool/internal/model"
)

const maxStderrBytes = 8 * 1024

type AgentRequest struct {
	ChatID              string
	Backend             chatmodel.Backend
	Binary              string
	WorkingDirectory    string
	WritableDirectories []string
	NativeSessionID     string
	NativeSessionPath   string
	Prompt              string
	Model               string
	ReasoningEffort     string
	PermissionMode      string
	Attachments         []chatmodel.AgentAttachment
	Files               []chatmodel.AgentAttachment
	Approve             chatmodel.ApprovalHandler
}

// AgentEvent types: session, turn, model, delta, message, retract, part,
// activity, compaction, and tool (a call already shown, now with its outcome).
type AgentEvent struct {
	Type            string          `json:"type"`
	Compacting      bool            `json:"compacting,omitempty"`
	TurnID          string          `json:"turn_id,omitempty"`
	Text            string          `json:"text,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	Model           string          `json:"model,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Part            *chatmodel.Part `json:"part,omitempty"`
	RetractedIDs    []string        `json:"retracted_ids,omitempty"`
}

type AgentResult struct {
	TurnID          string
	SessionID       string
	Model           string
	ReasoningEffort string
	Text            string
	Activities      []string
	Parts           []chatmodel.Part
}

type AgentRunner interface {
	Binary(chatmodel.Backend) (string, error)
	Run(context.Context, AgentRequest, func(AgentEvent) error) (AgentResult, error)
}

func (r *CLIRunner) Binary(backend chatmodel.Backend) (string, error) {
	r.mu.Lock()
	override := r.binaryOverrides[backend]
	r.mu.Unlock()
	if override != "" {
		path, err := exec.LookPath(override)
		if err != nil {
			return "", fmt.Errorf("configured %s executable is unavailable: %w", backend, err)
		}
		return path, nil
	}
	variable := "SPOOL_CODEX_BIN"
	name := "codex"
	if backend == chatmodel.BackendClaude {
		variable = "SPOOL_CLAUDE_BIN"
		name = "claude"
	}
	if override := strings.TrimSpace(os.Getenv(variable)); override != "" {
		if path, err := exec.LookPath(override); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("%s points to an executable that cannot be found: %s", variable, override)
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, candidate := range commonAgentPaths(name) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s CLI is not installed or is not available on PATH", name)
}

func commonAgentPaths(name string) []string {
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".cargo", "bin", name),
		filepath.Join(home, ".npm-global", "bin", name),
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		paths = append(paths, filepath.Join(appdata, "npm", name+".cmd"))
	}
	return paths
}

// WorkingDirectory resolves and validates an existing project directory.
func WorkingDirectory(directory string) (string, error) {
	if directory == "" {
		directory = "."
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agent working directory is not a directory: %s", resolved)
	}
	return resolved, nil
}

func readJSONLines(reader io.Reader, use func([]byte) error) error {
	buffer := bufio.NewReaderSize(reader, 64*1024)
	for {
		line, err := buffer.ReadBytes('\n')
		if len(line) > 0 {
			if useErr := use(line); useErr != nil {
				return useErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func codexActivity(item map[string]any) string {
	if item == nil {
		return ""
	}
	kind := codexItemKind(item)
	switch kind {
	case "commandexecution", "localshellcall":
		command := codexCommandText(item)
		if command == "" {
			command = "command"
		}
		return "Ran " + chatmodel.Clip(command, 220)
	case "filechange":
		return "Updated workspace files"
	default:
		if title := stringValue(item["title"]); title != "" {
			return chatmodel.Clip(title, 240)
		}
		return "Working with " + kind
	}
}

func codexItemKind(item map[string]any) string {
	value := strings.ToLower(stringValue(item["type"]))
	return strings.NewReplacer("_", "", "-", "").Replace(value)
}

func stringValues(values []any, separator string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if text := stringValue(value); text != "" {
			items = append(items, text)
		}
	}
	return strings.Join(items, separator)
}

func codexCommandText(item map[string]any) string {
	if command := stringValue(item["command"]); command != "" {
		return command
	}
	if values, ok := item["command"].([]any); ok {
		return codexCommandArgs(values)
	}
	if action, ok := item["action"].(map[string]any); ok {
		if command := stringValue(action["command"]); command != "" {
			return command
		}
		if values, ok := action["command"].([]any); ok {
			return codexCommandArgs(values)
		}
	}
	return ""
}

// Keep argv boundaries when showing native command events as shell text, so
// a shell's -c script can still be recognized by the activity classifier.
func codexCommandArgs(values []any) string {
	args := make([]string, 0, len(values))
	for _, value := range values {
		arg, ok := value.(string)
		if !ok {
			continue
		}
		quoted := arg == ""
		for _, char := range arg {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-", char) {
				quoted = true
				break
			}
		}
		if quoted {
			arg = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
		}
		args = append(args, arg)
	}
	return strings.Join(args, " ")
}

func codexTranscriptPart(item map[string]any, model string) chatmodel.Part {
	parts := codexTranscriptParts(item, model)
	if len(parts) > 0 {
		return parts[0]
	}
	kind := stringValue(item["type"])
	return chatmodel.Part{
		Role:  "assistant",
		Kind:  "tool_use",
		Name:  kind,
		Model: model,
		Text:  codexActivity(item),
	}
}

// codexTranscriptParts accepts both app-server items and native rollout items.
// Keeping the conversion here makes the stored transcript independent of the
// agent protocol used to produce it.
func codexTranscriptParts(item map[string]any, model string) []chatmodel.Part {
	if codexCompactionItem(item) {
		return nil
	}
	if parts, ok := codexToolParts(item, model); ok {
		return parts
	}
	kind := codexItemKind(item)
	switch kind {
	case "message":
		role := stringValue(item["role"])
		if role != "user" && role != "assistant" {
			return nil
		}
		content, _ := item["content"].([]any)
		parts := make([]chatmodel.Part, 0, len(content))
		for _, value := range content {
			block, _ := value.(map[string]any)
			blockType := stringValue(block["type"])
			text := stringValue(block["text"])
			switch blockType {
			case "input_text", "output_text", "text":
				if text != "" {
					part := transcriptTextPart(role, text, model)
					if role == "assistant" {
						part.ID, part.Phase = stringValue(item["id"]), stringValue(item["phase"])
					}
					parts = append(parts, part)
				}
			case "image", "input_image":
				parts = append(parts, transcriptModelPart(chatmodel.Part{Role: role, Kind: "image", Text: "[image]", ImageURL: nativeImageURL(stringValue(block["image_url"]))}, model))
			}
		}
		if role == "user" {
			return projectUserParts(parts)
		}
		return parts
	case "reasoning":
		summary, _ := item["summary"].([]any)
		if len(summary) == 0 {
			summary, _ = item["summary_text"].([]any)
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
		if len(text) == 0 {
			if content, ok := item["content"].([]any); ok {
				for _, value := range content {
					// App-server content is a string array; rollout content uses
					// typed text blocks. Both carry the body when no summary exists.
					if value, ok := value.(string); ok {
						if value != "" {
							text = append(text, value)
						}
						continue
					}
					block, _ := value.(map[string]any)
					for _, key := range []string{"text", "reasoning_text", "summary_text"} {
						if value := stringValue(block[key]); value != "" {
							text = append(text, value)
							break
						}
					}
				}
			}
		}
		if len(text) == 0 {
			return nil
		}
		return []chatmodel.Part{transcriptModelPart(chatmodel.Part{ID: stringValue(item["id"]), Role: "assistant", Kind: "thinking", Text: strings.Join(text, "\n\n")}, model)}
	case "functioncall":
		return []chatmodel.Part{{ID: stringValue(item["call_id"]), Role: "assistant", Kind: "tool_use", Name: stringValue(item["name"]), Text: prettyJSONText(stringValue(item["arguments"])), Model: model}}
	case "customtoolcall":
		return []chatmodel.Part{{ID: stringValue(item["call_id"]), Role: "assistant", Kind: "tool_use", Name: stringValue(item["name"]), Text: stringValue(item["input"]), Model: model}}
	case "localshellcall":
		return []chatmodel.Part{{ID: stringValue(item["id"]), Role: "assistant", Kind: "tool_use", Name: "shell", Text: codexCommandText(item), Model: model}}
	case "imageview":
		if path := stringValue(item["path"]); path != "" {
			input, _ := json.Marshal(map[string]string{"path": path})
			return []chatmodel.Part{{ID: stringValue(item["id"]), Role: "assistant", Kind: "tool_use", Name: "view_image", Text: string(input), Model: model}}
		}
		return nil
	case "functioncalloutput", "customtoolcalloutput":
		return []chatmodel.Part{{ID: stringValue(item["call_id"]), Role: "tool", Kind: "tool_result", Text: codexTranscriptOutput(item["output"]), Images: codexToolImages(item["output"])}}
	default:
		return nil
	}
}

func codexCommandOutput(item map[string]any) string {
	for _, key := range []string{"aggregated_output", "aggregatedOutput", "formatted_output", "formattedOutput"} {
		if text := codexTranscriptOutput(item[key]); text != "" {
			return text
		}
	}
	var output []string
	for _, key := range []string{"stdout", "stderr"} {
		if text := codexTranscriptOutput(item[key]); text != "" {
			output = append(output, text)
		}
	}
	if len(output) > 0 {
		return strings.Join(output, "\n")
	}
	for _, key := range []string{"output", "result"} {
		if text := codexTranscriptOutput(item[key]); text != "" {
			return text
		}
	}
	return ""
}

// codexFileChangeDiff is a file change as one unified diff of all its files.
func codexFileChangeDiff(item map[string]any) string {
	var diffs []string
	for _, change := range codexFileChanges(item) {
		if change.diff != "" {
			diffs = append(diffs, change.diff)
		}
	}
	if len(diffs) > 0 {
		return strings.Join(diffs, "\n")
	}
	// Older records carry one combined patch instead of per-file changes.
	if diff := stringValue(item["diff"]); diff != "" {
		return diff
	}
	return stringValue(item["patch"])
}

func transcriptTextPart(role, text, model string) chatmodel.Part {
	return transcriptModelPart(chatmodel.Part{Role: role, Kind: "text", Text: text}, model)
}

func transcriptModelPart(part chatmodel.Part, model string) chatmodel.Part {
	if part.Role == "assistant" {
		part.Model = model
	}
	return part
}

func prettyJSONText(text string) string {
	if text == "" || len(text) > 4000 {
		return text
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, []byte(text), "", "  ") == nil {
		return formatted.String()
	}
	return text
}

func codexTranscriptOutput(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if values, ok := value.([]any); ok {
		var output []string
		for _, item := range values {
			if text := codexTranscriptOutput(item); text != "" {
				output = append(output, text)
			}
		}
		return strings.Join(output, "\n")
	}
	if object, ok := value.(map[string]any); ok {
		switch stringValue(object["type"]) {
		case "input_image", "output_image", "image":
			return "[image]"
		}
		for _, key := range []string{"content", "output", "text", "aggregated_output", "aggregatedOutput", "stdout", "stderr"} {
			if text := codexTranscriptOutput(object[key]); text != "" {
				return text
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

func codexToolImages(value any) []string {
	var images []string
	switch value := value.(type) {
	case []any:
		for _, block := range value {
			images = append(images, codexToolImages(block)...)
		}
	case map[string]any:
		switch stringValue(value["type"]) {
		case "input_image", "output_image", "image":
			if url := nativeImageURL(stringValue(value["image_url"])); url != "" {
				images = append(images, url)
			}
		default:
			for _, key := range []string{"content", "output"} {
				images = append(images, codexToolImages(value[key])...)
			}
		}
	}
	return images
}

func claudeToolResultPart(item map[string]any) chatmodel.Part {
	parts := claudeTranscriptParts("user", []any{map[string]any{
		"type": "tool_result", "content": item["content"], "tool_use_id": item["tool_use_id"], "is_error": item["is_error"],
	}}, "")
	if len(parts) > 0 {
		return parts[0]
	}
	return chatmodel.Part{}
}

func claudeToolUsePart(item map[string]any, model string) chatmodel.Part {
	parts := claudeTranscriptParts("assistant", []any{item}, model)
	if len(parts) > 0 {
		return parts[0]
	}
	return chatmodel.Part{}
}

func claudeTranscriptParts(role string, content any, model string) []chatmodel.Part {
	if text, ok := content.(string); ok {
		parts := []chatmodel.Part{transcriptTextPart(role, text, model)}
		if role == "user" {
			return projectUserParts(parts)
		}
		return parts
	}
	blocks, _ := content.([]any)
	parts := make([]chatmodel.Part, 0, len(blocks))
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		switch stringValue(block["type"]) {
		case "text":
			if text := stringValue(block["text"]); text != "" {
				parts = append(parts, transcriptTextPart(role, text, model))
			}
		case "thinking":
			if text := stringValue(block["thinking"]); text != "" {
				parts = append(parts, transcriptModelPart(chatmodel.Part{Role: role, Kind: "thinking", Text: text}, model))
			}
		case "tool_use":
			if role == "assistant" {
				name := stringValue(block["name"])
				text := ""
				if input, ok := block["input"]; ok && input != nil {
					if encoded, err := json.MarshalIndent(input, "", "  "); err == nil && string(encoded) != "null" {
						text = string(encoded)
					}
				}
				parts = append(parts, chatmodel.Part{ID: stringValue(block["id"]), Role: role, Kind: "tool_use", Name: name, Model: model, Text: text})
			}
		case "tool_result":
			var text string
			var images []string
			switch result := block["content"].(type) {
			case string:
				text = result
			case []any:
				var content []string
				for _, resultBlock := range result {
					entry, _ := resultBlock.(map[string]any)
					switch stringValue(entry["type"]) {
					case "text":
						if value := stringValue(entry["text"]); value != "" {
							content = append(content, value)
						}
					case "image":
						content = append(content, "[image]")
						source, _ := entry["source"].(map[string]any)
						if source["type"] == "base64" {
							if url := nativeImageURL("data:" + stringValue(source["media_type"]) + ";base64," + stringValue(source["data"])); url != "" {
								images = append(images, url)
							}
						}
					}
				}
				text = strings.Join(content, "\n")
			}
			if text != "" || block["is_error"] != nil {
				part := chatmodel.Part{ID: stringValue(block["tool_use_id"]), Role: "tool", Kind: "tool_result", Text: text, Images: images}
				if failed, ok := block["is_error"].(bool); ok {
					part.Native = &chatmodel.ToolNative{Status: chatmodel.ToolCompleted}
					if failed {
						part.Native.Status = chatmodel.ToolFailed
					}
				}
				parts = append(parts, part)
			}
		case "image":
			source, _ := block["source"].(map[string]any)
			url := ""
			if source["type"] == "base64" {
				url = nativeImageURL("data:" + stringValue(source["media_type"]) + ";base64," + stringValue(source["data"]))
			}
			parts = append(parts, transcriptModelPart(chatmodel.Part{Role: role, Kind: "image", Text: "[image]", ImageURL: url}, model))
		}
	}
	if role == "user" {
		return projectUserParts(parts)
	}
	return parts
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return stringValue(current)
}

func processError(name string, err error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if detail != "" {
		return fmt.Errorf("%s exited with an error: %s", name, chatmodel.Clip(detail, maxStderrBytes))
	}
	return fmt.Errorf("%s exited with an error: %w", name, err)
}

func contextError(ctx context.Context) error {
	err := context.Cause(ctx)
	if err == context.DeadlineExceeded {
		return fmt.Errorf("agent timed out: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("agent stopped; no result was imported: %w", err)
	}
	return err
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.Buffer.Write(data)
	}
	return n, nil
}

// Never turn native local paths or arbitrary URLs into browser image fetches.
func nativeImageURL(value string) string {
	for _, kind := range []string{"png", "jpeg", "gif", "webp"} {
		if strings.HasPrefix(value, "data:image/"+kind+";base64,") {
			return value
		}
	}
	return ""
}
