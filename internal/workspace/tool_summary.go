package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

var (
	toolPatchName  = regexp.MustCompile(`(?:^|\.|__)(?:apply_?patch|patch|file_?change)$`)
	toolPatchFile  = regexp.MustCompile(`(?m)^\*\*\* (Add|Update|Delete) File: (.+)\r?$`)
	toolPath       = regexp.MustCompile(`(?:^|\s)([\w./~-]+\.[\w-]+)(?:\s|$)`)
	toolPatchStart = regexp.MustCompile(`(?:^|\n)(?:diff --git |--- [^\n]+\n\+\+\+ |\*\*\* Begin Patch)`)
)

func toolPart(part chatmodel.Part) bool {
	return part.Role != "user" && (part.Kind == "tool_use" || part.Kind == "tool_result" || part.Kind == "thinking")
}

func toolBodyVersion(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func toolBounded(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// Specific provider classifications win. A generic native shell classification
// still uses the literal command fallback, including its compound steps.
// tool-activity.tsx and Android keep the same fallback rules and localize labels.
func summarizeTool(part chatmodel.Part) *chatmodel.ToolSummary {
	if part.Tool != nil && part.Tool.SourceText == part.Text && part.Tool.ImageCount == len(part.Images) && part.Tool.SourceNative == part.Native {
		copy := *part.Tool
		return &copy
	}
	summary := &chatmodel.ToolSummary{Version: toolBodyVersion(part.Text), Bytes: len(part.Text), Kind: "other", SourceText: part.Text, SourceNative: part.Native}
	summary.ImageCount = len(part.Images)
	native := part.Native
	if native == nil {
		native = &chatmodel.ToolNative{}
	}
	summary.Status, summary.ExitCode, summary.DurationMs = native.Status, native.ExitCode, native.DurationMs
	summary.Task = native.Task
	if native.Kind == "task" {
		// A task's name is the title its author gave it and its text is prose
		// about the work: neither names a tool, a path or a change.
		summary.Kind = "task"
		summary.Task = summarizeTaskSteps(native.Task, native.Status)
		return summary
	}
	if part.Kind == "tool_result" {
		return summarizeToolChanges(part.Text, summary)
	}
	name := strings.ToLower(strings.TrimSpace(part.Name))
	nameKind := toolNameKind(name)
	var data map[string]json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(part.Text), "{") {
		_ = json.Unmarshal([]byte(part.Text), &data)
	}
	images := viewedImagePaths(name, part.Text)
	files := [][]string(nil)
	if toolPatchName.MatchString(name) {
		files = toolPatchFile.FindAllStringSubmatch(part.Text, -1)
	}
	query := ""
	switch {
	case part.Kind == "thinking" || nameKind == "thinking":
		summary.Kind = "thinking"
		summary.Preview = toolBounded(part.Text, 2048)
	case len(images) > 0:
		summary.Kind, summary.ImagePaths = "image", images
	case len(native.Files) > 0:
		// A native file change says what happened to each file, whichever tool
		// (a patch tool or a shell heredoc) the model used to make it.
		summary.Kind = "write"
		allAdd, allDelete := true, true
		changes := chatmodel.ToolChanges{}
		for _, file := range native.Files {
			allAdd = allAdd && file.Kind == "create"
			allDelete = allDelete && file.Kind == "delete"
			changes.Additions += file.Additions
			changes.Deletions += file.Deletions
		}
		if allAdd {
			summary.Kind = "create"
		} else if allDelete {
			summary.Kind = "delete"
		}
		if len(native.Files) <= 64 {
			summary.Files = make([]chatmodel.ToolFile, len(native.Files))
			for i, file := range native.Files {
				file.Path, file.MovePath = toolBounded(file.Path, 1024), toolBounded(file.MovePath, 1024)
				summary.Files[i] = file
			}
		}
		if len(native.Files) == 1 {
			summary.Path = native.Files[0].Path
		}
		files = nil
		summary.Changes = &changes
	case toolPatchName.MatchString(name):
		summary.Kind = "write"
		allAdd, allDelete := len(files) > 0, len(files) > 0
		for _, file := range files {
			allAdd = allAdd && file[1] == "Add"
			allDelete = allDelete && file[1] == "Delete"
		}
		if allAdd {
			summary.Kind = "create"
		} else if allDelete {
			summary.Kind = "delete"
		}
	case nameKind == "shell" && (native.Kind == "" || native.Kind == "shell" && len(native.Operations) == 0):
		command := toolCommand(part.Text, data)
		summary.Kind = shellCommandKind(command, 0)
		summary.Operations = shellOperations(command, 0)
		if summary.Kind == "read" || summary.Kind == "list" || summary.Kind == "search" {
			summary.Path, query = shellTarget(command, 0)
		}
	case native.Kind != "":
		summary.Kind, summary.Path, query = native.Kind, native.Path, native.Query
		summary.Operations = boundedOperations(native.Operations)
	default:
		summary.Kind = nameKind
	}
	field := func(keys ...string) string {
		for _, key := range keys {
			var value string
			if json.Unmarshal(data[key], &value) == nil && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	if summary.Path == "" && len(native.Files) == 0 {
		summary.Path = field("file_path", "path", "file", "filename", "target_file", "notebook_path")
	}
	if summary.Path == "" {
		if len(files) == 1 {
			summary.Path = strings.TrimSpace(files[0][2])
		} else if len(files) == 0 && nameKind != "shell" && len(native.Files) == 0 && native.Kind == "" {
			if match := toolPath.FindStringSubmatch(part.Text); match != nil {
				summary.Path = match[1]
			}
		}
	}
	// Shorten paths before bounding them so ordinary deep paths retain the Web label.
	pieces := strings.FieldsFunc(strings.ReplaceAll(summary.Path, `\`, "/"), func(r rune) bool { return r == '/' })
	if len(pieces) > 3 {
		summary.Path = "\u2026/" + strings.Join(pieces[len(pieces)-3:], "/")
	}
	summary.Path = toolBounded(summary.Path, 1024)
	if query == "" {
		query = field("pattern", "query", "search_query", "searchTerm", "glob", "include")
	}
	summary.Query = toolBounded(query, 1024)
	if summary.Changes != nil {
		summary.Stats = toolChangeSuffix(part.Text)
		return summary
	}
	return summarizeToolChanges(part.Text, summary)
}

// Operation rows are display metadata; a command too large to bound is shown
// as one call instead of being cut.
func boundedOperations(operations []chatmodel.ToolOperation) []chatmodel.ToolOperation {
	if len(operations) < 2 || len(operations) > 64 {
		return nil
	}
	for _, operation := range operations {
		if len(operation.Command) > 512 || len(operation.Path) > 1024 || len(operation.Query) > 1024 {
			return nil
		}
	}
	return append([]chatmodel.ToolOperation(nil), operations...)
}

func summarizeToolChanges(text string, summary *chatmodel.ToolSummary) *chatmodel.ToolSummary {
	summary.Stats = toolChangeSuffix(text)
	// Most results are logs, not patches. Avoid the regexp interpreter scanning
	// megabytes of plain output (particularly costly under the race detector).
	if !strings.Contains(text, "diff --git ") && !strings.Contains(text, "--- ") && !strings.Contains(text, "*** Begin Patch") {
		return summary
	}
	if start := toolPatchStart.FindStringIndex(text); start != nil {
		patch := text[start[0]:]
		if strings.Contains(patch, "@@") || strings.Contains(patch, "*** Begin Patch") {
			summary.Changes = &chatmodel.ToolChanges{}
			for line := range strings.SplitSeq(patch, "\n") {
				if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
					continue
				}
				if strings.HasPrefix(line, "+") {
					summary.Changes.Additions++
				} else if strings.HasPrefix(line, "-") {
					summary.Changes.Deletions++
				}
			}
		}
	}
	return summary
}

func toolChangeSuffix(text string) string {
	space := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	end := len(text)
	for end > 0 && space(text[end-1]) {
		end--
	}
	i := end
	for i > 0 && digit(text[i-1]) {
		i--
	}
	if i == end || i == 0 || text[i-1] != '-' {
		return ""
	}
	minus := i - 1
	i = minus
	for i > 0 && space(text[i-1]) {
		i--
	}
	if i == minus {
		return ""
	}
	plusEnd := i
	for i > 0 && digit(text[i-1]) {
		i--
	}
	if i == plusEnd || i == 0 || text[i-1] != '+' {
		return ""
	}
	plus := i - 1
	if plus > 0 && !space(text[plus-1]) {
		return ""
	}
	return text[plus:plusEnd] + " " + text[minus:end]
}

func toolPartRef(chatID string, turn chatmodel.Turn, index int, part chatmodel.Part) string {
	if turn.NativeHistory && turn.NativeTurnID != "" && part.ID != "" {
		return nativeToolRef(chatID, nativePartTurnID(turn, part), part)
	}
	return toolBodyVersion(chatID + ":" + turn.ID + ":" + strconv.Itoa(index-turn.PartStart) + ":" + part.Kind + ":" + part.ID)
}

func nativePartTurnID(turn chatmodel.Turn, part chatmodel.Part) string {
	if part.NativeTurnID != "" {
		return part.NativeTurnID
	}
	return turn.NativeTurnID
}

// Projection never changes the provider-owned snapshot or the live runner state.
func slimToolDetails(chat chatmodel.Session) chatmodel.Session {
	chat.Transcript.Parts = append([]chatmodel.Part{}, chat.Transcript.Parts...)
	for _, turn := range chat.Turns {
		for i := max(0, turn.PartStart); i < min(turn.PartEnd, len(chat.Transcript.Parts)); i++ {
			part := &chat.Transcript.Parts[i]
			if !toolPart(*part) {
				continue
			}
			part.Tool = summarizeTool(*part)
			part.Tool.Ref = toolPartRef(chat.ID, turn, i, *part)
			if part.Tool.Kind == "task" {
				// Task events carry bounded progress metadata, not raw tool I/O.
				// Keep it with the live row: a foreground fork may not have
				// written a main-transcript turn from which details can load yet.
				part.Tool.Href, part.Tool.SourceText = "", ""
				continue
			}
			part.Tool.Href = "/chats/" + chat.ID + "/tool-detail?ref=" + part.Tool.Ref
			part.Tool.ImageURLs = nil
			imageCount := len(part.Images)
			if turn.NativeHistory && part.Kind == "tool_use" && part.Tool.Kind == "image" {
				imageCount = max(imageCount, len(part.Tool.ImagePaths))
			}
			for index := range imageCount {
				part.Tool.ImageURLs = append(part.Tool.ImageURLs, "/api/chats/"+chat.ID+"/tool-image?ref="+part.Tool.Ref+"&index="+strconv.Itoa(index))
			}
			part.Tool.SourceText = ""
			part.Text = ""
			part.Images = nil
		}
	}
	return chat
}

// A child's steps are read the way its parent's calls are, so that both lists
// name the same call the same way. A step keeps its command only when nothing
// else says what it was for. One that was still running when its child ended
// was never told to have finished: it says nothing of how it went.
func summarizeTaskSteps(task *chatmodel.TaskProgress, status string) *chatmodel.TaskProgress {
	if task == nil || len(task.Steps) == 0 {
		return task
	}
	summarized := *task
	summarized.Steps = make([]chatmodel.TaskStep, len(task.Steps))
	for i, step := range task.Steps {
		if step.Kind != chatmodel.TaskStepMessage {
			call := chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: step.Name, Text: step.Input}
			if step.Kind != "" {
				call.Native = &chatmodel.ToolNative{Kind: step.Kind, Path: step.Path, Query: step.Query}
			}
			tool := summarizeTool(call)
			step.Kind, step.Path, step.Query, step.Text = tool.Kind, tool.Path, tool.Query, ""
			if step.Kind == "image" {
				// A list row names the file; the image itself is not carried.
				step.Kind = "read"
				if len(tool.ImagePaths) > 0 {
					step.Path = toolBounded(tool.ImagePaths[0], 1024)
				}
			}
			if step.Path == "" && step.Query == "" && toolNameKind(step.Name) == "shell" {
				var data map[string]json.RawMessage
				if strings.HasPrefix(strings.TrimSpace(step.Input), "{") {
					_ = json.Unmarshal([]byte(step.Input), &data)
				}
				step.Text = toolBounded(strings.Join(strings.Fields(toolCommand(step.Input, data)), " "), 240)
			}
		}
		if status != chatmodel.ToolRunning && step.Status == chatmodel.ToolRunning {
			step.Status = ""
		}
		summarized.Steps[i] = step
	}
	return &summarized
}
