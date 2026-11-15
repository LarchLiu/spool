package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	chatmodel "spool/internal/model"
)

// A native Codex tool item is read in two spellings: app-server's (camelCase,
// in live notifications and thread history) and the rollout's item_completed
// record (snake_case). Both become the same call and result, paired by the
// item's ID, with the provider's own structured facts kept beside the text.

func itemField(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func codexToolStatus(value any) string {
	switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(stringValue(value))) {
	case "inprogress":
		return chatmodel.ToolRunning
	case "completed":
		return chatmodel.ToolCompleted
	case "failed":
		return chatmodel.ToolFailed
	case "declined":
		return chatmodel.ToolDeclined
	}
	return ""
}

func codexDurationMs(item map[string]any) int64 {
	if ms, ok := numberValue(itemField(item, "durationMs", "duration_ms")); ok {
		return int64(ms)
	}
	duration, _ := item["duration"].(map[string]any)
	secs, _ := duration["secs"].(float64)
	nanos, _ := duration["nanos"].(float64)
	return int64(secs)*1000 + int64(nanos)/1_000_000
}

func codexJSONText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return prettyJSONText(text)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

// codexToolParts returns nil, false for an item that is not a native tool item.
func codexToolParts(item map[string]any, model string) ([]chatmodel.Part, bool) {
	id := stringValue(item["id"])
	call := chatmodel.Part{ID: id, Role: "assistant", Kind: "tool_use", Model: model}
	result := chatmodel.Part{ID: id, Role: "tool", Kind: "tool_result"}
	native := &chatmodel.ToolNative{Status: codexToolStatus(item["status"]), DurationMs: codexDurationMs(item)}
	switch codexItemKind(item) {
	case "commandexecution":
		call.Name, call.Text = "shell", codexCommandText(item)
		if call.Text == "" {
			call.Text = codexActivity(item)
		}
		if code, ok := numberValue(itemField(item, "exitCode", "exit_code")); ok {
			native.ExitCode = &code
		}
		codexCommandActions(itemField(item, "commandActions", "parsed_cmd"), native)
		result.Text = codexCommandOutput(item)
	case "filechange":
		call.Name = "fileChange"
		var list []string
		for _, change := range codexFileChanges(item) {
			list = append(list, change.header)
			native.Files = append(native.Files, change.file)
		}
		call.Text = strings.Join(list, "\n")
		if call.Text == "" {
			if call.Text = stringValue(item["title"]); call.Text == "" {
				call.Text = "File change"
			}
		}
		result.Text = codexFileChangeDiff(item)
	case "mcptoolcall":
		call.Name = strings.Trim(stringValue(item["server"])+"."+stringValue(item["tool"]), ".")
		call.Text = codexJSONText(item["arguments"])
		output, _ := item["result"].(map[string]any)
		if output["isError"] == true {
			native.Status = chatmodel.ToolFailed
		}
		result.Text, result.Images = codexMCPContent(output["content"])
		if result.Text == "" && output["structuredContent"] != nil {
			result.Text = codexJSONText(output["structuredContent"])
		}
		if failure, _ := item["error"].(map[string]any); failure != nil {
			native.Status = chatmodel.ToolFailed
			if message := stringValue(failure["message"]); message != "" {
				result.Text = strings.TrimSpace(result.Text + "\n" + message)
			}
		}
	case "dynamictoolcall":
		call.Name = strings.Trim(stringValue(item["namespace"])+"."+stringValue(item["tool"]), ".")
		call.Text = codexJSONText(item["arguments"])
		contents, _ := itemField(item, "contentItems", "content_items").([]any)
		var text []string
		for _, value := range contents {
			block, _ := value.(map[string]any)
			if value := stringValue(block["text"]); value != "" {
				text = append(text, value)
			}
			if url := nativeImageURL(stringValue(itemField(block, "imageUrl", "image_url"))); url != "" {
				text = append(text, "[image]")
				result.Images = append(result.Images, url)
			}
		}
		result.Text = strings.Join(text, "\n")
		if item["success"] == false {
			native.Status = chatmodel.ToolFailed
		}
	case "websearch", "websearchcall":
		call.Name, native.Kind = "web_search", "web"
		action, _ := item["action"].(map[string]any)
		input := map[string]any{}
		native.Query = stringValue(item["query"])
		if native.Query == "" {
			native.Query = stringValue(action["query"])
		}
		if native.Query != "" {
			input["query"] = native.Query
		}
		for _, key := range []string{"queries", "url", "pattern"} {
			if action[key] != nil {
				input[key] = action[key]
			}
		}
		// A search is recorded once it has finished; it has no input beyond these.
		call.Text = codexJSONText(input)
	case "collabagenttoolcall":
		call.Name = snakeCase(stringValue(item["tool"]))
		input := map[string]any{}
		for _, key := range []string{"prompt", "model", "receiverThreadIds"} {
			if value := item[key]; value != nil && value != "" {
				input[key] = value
			}
		}
		call.Text = codexJSONText(input)
		states, _ := itemField(item, "agentsStates", "agents_states").(map[string]any)
		threads := make([]string, 0, len(states))
		for thread := range states {
			threads = append(threads, thread)
		}
		sort.Strings(threads)
		var text []string
		for _, thread := range threads {
			state, _ := states[thread].(map[string]any)
			if message := stringValue(state["message"]); message != "" {
				text = append(text, stringValue(state["status"])+": "+message)
			}
		}
		result.Text = strings.Join(text, "\n")
	case "imagegeneration":
		call.Name = "image_generation"
		input := map[string]any{}
		if prompt := stringValue(itemField(item, "revisedPrompt", "revised_prompt")); prompt != "" {
			input["prompt"] = prompt
		}
		if path := stringValue(itemField(item, "savedPath", "saved_path")); path != "" {
			input["path"] = path
		}
		call.Text = codexJSONText(input)
		if url := nativeImageURL("data:image/png;base64," + stringValue(item["result"])); stringValue(item["result"]) != "" && url != "" {
			result.Text, result.Images = "[image]", []string{url}
		}
		if item["failure"] != nil {
			native.Status = chatmodel.ToolFailed
		}
	case "sleep":
		call.Name = "sleep"
		call.Text = codexJSONText(map[string]any{"duration_ms": native.DurationMs})
		native.DurationMs = 0
	case "functioncalloutput":
		// A tool without an item type of its own is recorded by its output alone.
		if id == "" || item["call_id"] != nil || stringValue(item["name"]) == "" {
			return nil, false
		}
		call.Name = strings.Trim(stringValue(item["namespace"])+"."+stringValue(item["name"]), ".")
		result.Text, result.Images = codexTranscriptOutput(item["output"]), codexToolImages(item["output"])
	default:
		return nil, false
	}
	if native.Kind != "" || native.Path != "" || native.Query != "" || native.Status != "" || native.ExitCode != nil || native.DurationMs != 0 || len(native.Operations) > 0 || len(native.Files) > 0 {
		call.Native = native
	}
	if native.Status != "" && native.Status != chatmodel.ToolRunning {
		result.Native = &chatmodel.ToolNative{Status: native.Status}
	}
	parts := []chatmodel.Part{call}
	if result.Text != "" || len(result.Images) > 0 || result.Native != nil {
		parts = append(parts, result)
	}
	return parts, true
}

func snakeCase(name string) string {
	var out strings.Builder
	for i, char := range name {
		if unicode.IsUpper(char) {
			if i > 0 {
				out.WriteByte('_')
			}
			char = unicode.ToLower(char)
		}
		out.WriteRune(char)
	}
	return out.String()
}

// codexCommandActions keeps Codex's own reading of a command. Codex reports a
// single unknown action whenever any step is not a read, listing or search;
// preserve that generic native fact. The display summary can still classify
// the literal command and its individual steps when native actions are unknown.
func codexCommandActions(value any, native *chatmodel.ToolNative) {
	actions, _ := value.([]any)
	var operations []chatmodel.ToolOperation
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		operation := chatmodel.ToolOperation{Command: stringValue(itemField(action, "command", "cmd")), Path: stringValue(action["path"]), Query: stringValue(action["query"])}
		switch strings.ToLower(strings.ReplaceAll(stringValue(action["type"]), "_", "")) {
		case "read":
			operation.Kind, operation.Query = "read", ""
			if operation.Path == "" {
				operation.Path = stringValue(action["name"])
			}
		case "listfiles":
			operation.Kind, operation.Query = "list", ""
		case "search":
			operation.Kind = "search"
		case "unknown":
			operation.Kind, operation.Path, operation.Query = "shell", "", ""
		default:
			native.Kind = "shell"
			return
		}
		operations = append(operations, operation)
	}
	if len(operations) == 0 {
		return
	}
	native.Kind = operations[0].Kind
	for _, operation := range operations[1:] {
		if operation.Kind != native.Kind {
			native.Kind = "shell"
		}
	}
	if len(operations) == 1 {
		native.Path, native.Query = operations[0].Path, operations[0].Query
		return
	}
	native.Operations = operations
}

type codexFileChange struct {
	file   chatmodel.ToolFile
	header string // the change as an apply_patch file header
	diff   string // a complete unified diff of this file, or ""
}

// codexFileChanges reads a file change's files in a stable order. A change's
// kind is a string, or an object naming its type; an added or deleted file
// carries its content where an updated one carries hunks.
func codexFileChanges(item map[string]any) []codexFileChange {
	changes, _ := item["changes"].([]any)
	if byPath, ok := item["changes"].(map[string]any); ok {
		paths := make([]string, 0, len(byPath))
		for path := range byPath {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			change, _ := byPath[path].(map[string]any)
			if change == nil {
				change = map[string]any{}
				if text, ok := byPath[path].(string); ok {
					change["diff"] = text
				}
			} else {
				copied := make(map[string]any, len(change)+1)
				for key, value := range change {
					copied[key] = value
				}
				change = copied
			}
			if stringValue(change["path"]) == "" {
				change["path"] = path
			}
			changes = append(changes, change)
		}
	}
	out := make([]codexFileChange, 0, len(changes))
	for _, raw := range changes {
		change, _ := raw.(map[string]any)
		if change == nil {
			continue
		}
		path := stringValue(itemField(change, "path", "file_path", "filePath"))
		kind, move := stringValue(change["type"]), stringValue(change["move_path"])
		nativeContent := false
		switch value := change["kind"].(type) {
		case string:
			kind = value
		case map[string]any:
			nativeContent = true // app-server add/delete diff is raw file content
			kind = stringValue(value["type"])
			if move == "" {
				move = stringValue(value["move_path"])
			}
		}
		patch := ""
		for _, key := range []string{"unified_diff", "diff", "patch"} {
			if patch = stringValue(change[key]); patch != "" {
				break
			}
		}
		content, hasContent := "", false
		for _, key := range []string{"content", "new_content", "newContent", "newString"} {
			if _, ok := change[key].(string); ok {
				content, hasContent = stringValue(change[key]), true
				break
			}
		}
		old := ""
		for _, key := range []string{"old_content", "oldContent", "originalFile", "original_file"} {
			if old = stringValue(change[key]); old != "" {
				break
			}
		}
		entry := codexFileChange{file: chatmodel.ToolFile{Path: path, MovePath: move}}
		name := path
		if name == "" {
			name = "file"
		}
		head := func(from, to string) string {
			return "diff --git a/" + name + " b/" + name + "\n--- " + from + "\n+++ " + to + "\n"
		}
		whole := func(text, sign string) (string, int) {
			lines := prefixedLines(text, sign)
			if text == "" {
				lines = nil
			}
			return strings.Join(lines, "\n"), len(lines)
		}
		looksLikePatch := strings.HasPrefix(patch, "diff --git ") || strings.HasPrefix(patch, "--- ") || strings.HasPrefix(patch, "@@")
		switch strings.ToLower(kind) {
		case "add", "create", "created":
			entry.file.Kind, entry.header = "create", "*** Add File: "+path
			if !hasContent && (nativeContent || !looksLikePatch) {
				_, hasDiff := change["diff"].(string)
				content, hasContent, patch = patch, hasDiff || patch != "", ""
			}
			if hasContent {
				body, count := whole(content, "+")
				entry.file.Additions = count
				entry.diff = head("/dev/null", "b/"+name) + fmt.Sprintf("@@ -0,0 +1,%d @@", count)
				if body != "" {
					entry.diff += "\n" + body
				}
			}
		case "delete", "remove", "deleted":
			entry.file.Kind, entry.header = "delete", "*** Delete File: "+path
			if !hasContent && old == "" && (nativeContent || !looksLikePatch) {
				_, hasDiff := change["diff"].(string)
				content, hasContent, patch = patch, hasDiff || patch != "", ""
			}
			if old == "" {
				old = content
			}
			if old != "" || hasContent {
				body, count := whole(old, "-")
				entry.file.Deletions = count
				entry.diff = head("a/"+name, "/dev/null") + fmt.Sprintf("@@ -1,%d +0,0 @@", count)
				if body != "" {
					entry.diff += "\n" + body
				}
			}
		default:
			entry.file.Kind, entry.header = "write", "*** Update File: "+path
			if move != "" {
				entry.header += "\n*** Move to: " + move
				// app-server appends the destination to a moved file's hunks.
				patch = strings.TrimSuffix(patch, "\n\nMoved to: "+move)
			}
			if patch == "" && (old != "" || hasContent) {
				removed, removedCount := whole(old, "-")
				added, addedCount := whole(content, "+")
				patch = fmt.Sprintf("@@ -1,%d +1,%d @@", removedCount, addedCount)
				for _, body := range []string{removed, added} {
					if body != "" {
						patch += "\n" + body
					}
				}
			}
		}
		if entry.diff == "" && patch != "" {
			entry.diff = patch
			if !strings.Contains(patch, "diff --git ") {
				entry.diff = head("a/"+name, "b/"+name) + patch
			}
			entry.file.Additions, entry.file.Deletions = 0, 0
			inHunk := false
			for _, line := range strings.Split(patch, "\n") {
				if strings.HasPrefix(line, "diff --git ") {
					inHunk = false
					continue
				}
				if strings.HasPrefix(line, "@@") {
					inHunk = true
					continue
				}
				if !inHunk {
					continue
				}
				if strings.HasPrefix(line, "+") {
					entry.file.Additions++
				} else if strings.HasPrefix(line, "-") {
					entry.file.Deletions++
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

func codexMCPContent(value any) (string, []string) {
	blocks, _ := value.([]any)
	var text, images []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		switch stringValue(block["type"]) {
		case "text":
			if value := stringValue(block["text"]); value != "" {
				text = append(text, value)
			}
		case "image":
			text = append(text, "[image]")
			if url := nativeImageURL("data:" + stringValue(itemField(block, "mimeType", "mime_type")) + ";base64," + stringValue(block["data"])); url != "" {
				images = append(images, url)
			}
		default:
			if encoded := codexJSONText(raw); encoded != "" {
				text = append(text, encoded)
			}
		}
	}
	return strings.Join(text, "\n"), images
}
