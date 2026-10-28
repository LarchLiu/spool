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
	"sort"
	"strings"

	chatmodel "spool/internal/model"
)

const maxStderrBytes = 8 * 1024

type AgentRequest struct {
	ChatID            string
	Backend           chatmodel.Backend
	Binary            string
	WorkingDirectory  string
	NativeSessionID   string
	NativeSessionPath string
	Prompt            string
	Model             string
	ReasoningEffort   string
	PermissionMode    string
	Attachments       []chatmodel.AgentAttachment
	Files             []chatmodel.AgentAttachment
	Approve           chatmodel.ApprovalHandler
}

type AgentEvent struct {
	Type            string          `json:"type"`
	TurnID          string          `json:"turn_id,omitempty"`
	Text            string          `json:"text,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	Model           string          `json:"model,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Part            *chatmodel.Part `json:"part,omitempty"`
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
		return stringValues(values, " ")
	}
	if action, ok := item["action"].(map[string]any); ok {
		if command := stringValue(action["command"]); command != "" {
			return command
		}
		if values, ok := action["command"].([]any); ok {
			return stringValues(values, " ")
		}
	}
	return ""
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
					parts = append(parts, transcriptTextPart(role, text, model))
				}
			case "image", "input_image":
				parts = append(parts, transcriptModelPart(chatmodel.Part{Role: role, Kind: "image", Text: "[image]", ImageURL: nativeImageURL(stringValue(block["image_url"]))}, model))
			}
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
		return []chatmodel.Part{transcriptModelPart(chatmodel.Part{Role: "assistant", Kind: "thinking", Text: strings.Join(text, "\n\n")}, model)}
	case "functioncall":
		return []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: stringValue(item["name"]), Text: prettyJSONText(stringValue(item["arguments"])), Model: model}}
	case "customtoolcall":
		return []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: stringValue(item["name"]), Text: stringValue(item["input"]), Model: model}}
	case "localshellcall":
		return []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: "shell", Text: codexCommandText(item), Model: model}}
	case "functioncalloutput", "customtoolcalloutput":
		return []chatmodel.Part{{Role: "tool", Kind: "tool_result", Text: codexTranscriptOutput(item["output"])}}
	case "commandexecution":
		command := codexCommandText(item)
		if command == "" {
			command = codexActivity(item)
		}
		parts := []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: "shell", Text: command, Model: model}}
		if output := codexCommandOutput(item); output != "" {
			parts = append(parts, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: output})
		}
		return parts
	case "filechange":
		text := stringValue(item["title"])
		if text == "" {
			text = codexActivity(item)
		}
		parts := []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Name: "fileChange", Text: text, Model: model}}
		if diff := codexFileChangeDiff(item); diff != "" {
			parts = append(parts, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: diff})
		}
		return parts
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

func codexFileChangeDiff(item map[string]any) string {
	if diff := stringValue(item["diff"]); diff != "" {
		return diff
	}
	if diff := stringValue(item["patch"]); diff != "" {
		return diff
	}
	changes, _ := item["changes"].([]any)
	if changesMap, ok := item["changes"].(map[string]any); ok {
		changes = make([]any, 0, len(changesMap))
		paths := make([]string, 0, len(changesMap))
		for path := range changesMap {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			value := changesMap[path]
			change, _ := value.(map[string]any)
			if change == nil {
				// Some Codex versions use a path -> patch/content map rather
				// than a path -> structured change object.
				if text, ok := value.(string); ok {
					change = map[string]any{"diff": text}
				} else {
					change = map[string]any{}
				}
			}
			if stringValue(change["path"]) == "" {
				change["path"] = path
			}
			changes = append(changes, change)
		}
	}
	var diffs []string
	for _, value := range changes {
		change, _ := value.(map[string]any)
		path := stringValue(change["path"])
		if path == "" {
			path = stringValue(change["file_path"])
		}
		if path == "" {
			path = stringValue(change["filePath"])
		}
		diff := codexChangeDiff(change, path)
		if diff == "" {
			continue
		}
		diffs = append(diffs, diff)
	}
	return strings.Join(diffs, "\n")
}

func codexChangeDiff(change map[string]any, path string) string {
	if change == nil {
		return ""
	}
	diff := stringValue(change["diff"])
	if diff == "" {
		diff = stringValue(change["patch"])
	}
	if diff == "" {
		diff = stringValue(change["unified_diff"])
	}
	if diff != "" {
		if path != "" && !strings.Contains(diff, "diff --git ") {
			diff = "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n" + diff
		}
		return diff
	}
	kind := strings.ToLower(stringValue(change["type"]))
	if kind == "" {
		kind = strings.ToLower(stringValue(change["kind"]))
	}
	content := codexTranscriptOutput(change["content"])
	if content == "" {
		content = codexTranscriptOutput(change["new_content"])
	}
	if content == "" {
		content = codexTranscriptOutput(change["newContent"])
	}
	if content == "" {
		content = codexTranscriptOutput(change["newString"])
	}
	old := codexTranscriptOutput(change["old_content"])
	if old == "" {
		old = codexTranscriptOutput(change["originalFile"])
	}
	if old == "" {
		old = codexTranscriptOutput(change["oldContent"])
	}
	if old == "" {
		old = codexTranscriptOutput(change["original_file"])
	}
	if path == "" {
		path = "file"
	}
	switch kind {
	case "add", "create", "created":
		lines := prefixedLines(content, "+")
		return strings.Join(append([]string{
			"diff --git a/" + path + " b/" + path,
			"--- /dev/null",
			"+++ b/" + path,
			fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)),
		}, lines...), "\n")
	case "delete", "remove", "deleted":
		lines := prefixedLines(oldOrContent(old, content), "-")
		return strings.Join(append([]string{
			"diff --git a/" + path + " b/" + path,
			"--- a/" + path,
			"+++ /dev/null",
			fmt.Sprintf("@@ -1,%d +0,0 @@", len(lines)),
		}, lines...), "\n")
	case "update", "edit", "modify", "modified", "change":
		if old == "" && content == "" {
			return ""
		}
		oldLines := prefixedLines(old, "-")
		newLines := prefixedLines(content, "+")
		return strings.Join(append([]string{
			"diff --git a/" + path + " b/" + path,
			"--- a/" + path,
			"+++ b/" + path,
			fmt.Sprintf("@@ -1,%d +1,%d @@", len(oldLines), len(newLines)),
		}, append(oldLines, newLines...)...), "\n")
	}
	return ""
}

func oldOrContent(old, content string) string {
	if old != "" {
		return old
	}
	return content
}

func transcriptTextPart(role, text, model string) chatmodel.Part {
	kind := "text"
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "# AGENTS.md") {
		kind = "context"
	}
	return transcriptModelPart(chatmodel.Part{Role: role, Kind: kind, Text: text}, model)
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

func claudeToolResultPart(item map[string]any) chatmodel.Part {
	parts := claudeTranscriptParts("user", []any{map[string]any{
		"type": "tool_result", "content": item["content"],
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
		return []chatmodel.Part{transcriptTextPart(role, text, model)}
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
				parts = append(parts, chatmodel.Part{Role: role, Kind: "tool_use", Name: name, Model: model, Text: text})
			}
		case "tool_result":
			var text string
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
					}
				}
				text = strings.Join(content, "\n")
			}
			if text != "" {
				parts = append(parts, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: text})
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
