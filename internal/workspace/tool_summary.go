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
	toolPatchName  = regexp.MustCompile(`(?:^|\.)(?:apply_?patch|patch|file_?change)$`)
	toolPatchFile  = regexp.MustCompile(`(?m)^\*\*\* (Add|Update|Delete) File: (.+)\r?$`)
	toolPath       = regexp.MustCompile(`(?:^|\s)([\w./~-]+\.[\w-]+)(?:\s|$)`)
	toolPatchStart = regexp.MustCompile(`(?:^|\n)(?:diff --git |--- [^\n]+\n\+\+\+ |\*\*\* Begin Patch)`)
	toolKindRules  = []struct {
		kind    string
		pattern *regexp.Regexp
	}{
		{"web", regexp.MustCompile(`websearch|web.?fetch|browser|internet|search the web|lookup url`)},
		{"list", regexp.MustCompile(`glob|list.?files|list files|directory listing|readdir|ls\b|find\b`)},
		{"search", regexp.MustCompile(`grep|ripgrep|rg\b|search files|search code|find in files|regex`)},
		{"read", regexp.MustCompile(`\bread\b|cat\b|head\b|tail\b|get.?file|open.?file|view file`)},
		{"create", regexp.MustCompile(`create|created|new file|add file|add_file`)},
		{"delete", regexp.MustCompile(`delete|deleted|remove|removed|unlink|delete_file`)},
		{"write", regexp.MustCompile(`write|edit|filechange|apply.?patch|patch|replace|create.?file|delete.?file`)},
		{"shell", regexp.MustCompile(`shell|bash|zsh|command|terminal|exec|run command`)},
	}
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

// Keep classification/paths/statistics identical to tool-activity.tsx. Only the
// localized labels belong to the clients; neither client needs the full JSON.
func summarizeTool(part chatmodel.Part) *chatmodel.ToolSummary {
	if part.Tool != nil && part.Tool.SourceText == part.Text && part.Tool.ImageCount == len(part.Images) {
		copy := *part.Tool
		return &copy
	}
	summary := &chatmodel.ToolSummary{Version: toolBodyVersion(part.Text), Bytes: len(part.Text), Kind: "other", SourceText: part.Text}
	summary.ImageCount = len(part.Images)
	if part.Kind == "tool_result" {
		return summarizeToolChanges(part.Text, summary)
	}
	name := strings.ToLower(strings.TrimSpace(part.Name))
	tool := name[strings.LastIndex(name, ".")+1:]
	images := viewedImagePaths(name, part.Text)
	files := [][]string(nil)
	if toolPatchName.MatchString(name) {
		files = toolPatchFile.FindAllStringSubmatch(part.Text, -1)
	}
	switch {
	case part.Kind == "thinking" || name == "reason" || name == "reasoning" || name == "think" || name == "thinking":
		summary.Kind = "thinking"
		summary.Preview = toolBounded(part.Text, 2048)
	case len(images) > 0:
		summary.Kind, summary.ImagePaths = "image", images
	case toolPatchName.MatchString(tool):
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
	case tool == "write" || tool == "write_file" || tool == "writefile" || tool == "create_file" || tool == "createfile":
		summary.Kind = "create"
	case tool == "edit" || tool == "edit_file" || tool == "editfile" || tool == "multi_edit" || tool == "multiedit" || tool == "replace":
		summary.Kind = "write"
	case tool == "delete_file" || tool == "deletefile" || tool == "remove_file" || tool == "removefile" || tool == "unlink":
		summary.Kind = "delete"
	default:
		source := name + " " + strings.ToLower(part.Text)
		for _, rule := range toolKindRules {
			if rule.pattern.MatchString(source) {
				summary.Kind = rule.kind
				break
			}
		}
	}
	var data map[string]json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(part.Text), "{") {
		_ = json.Unmarshal([]byte(part.Text), &data)
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
	summary.Path = field("file_path", "path", "file", "filename", "target_file", "notebook_path")
	if summary.Path == "" {
		if len(files) == 1 {
			summary.Path = strings.TrimSpace(files[0][2])
		} else if len(files) == 0 {
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
	summary.Query = toolBounded(field("pattern", "query", "search_query", "searchTerm", "glob", "include"), 1024)
	return summarizeToolChanges(part.Text, summary)
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
	return toolBodyVersion(chatID + ":" + turn.ID + ":" + strconv.Itoa(index-turn.PartStart) + ":" + part.Kind + ":" + part.ID)
}

// Projection never changes the provider-owned snapshot or the live runner state.
func slimToolDetails(chat chatmodel.Session) chatmodel.Session {
	chat.Transcript.Parts = append([]chatmodel.Part(nil), chat.Transcript.Parts...)
	for _, turn := range chat.Turns {
		for i := max(0, turn.PartStart); i < min(turn.PartEnd, len(chat.Transcript.Parts)); i++ {
			part := &chat.Transcript.Parts[i]
			if !toolPart(*part) {
				continue
			}
			part.Tool = summarizeTool(*part)
			part.Tool.Ref = toolPartRef(chat.ID, turn, i, *part)
			part.Tool.Href = "/chats/" + chat.ID + "/tool-detail?ref=" + part.Tool.Ref
			part.Tool.ImageURLs = nil
			for index := range part.Images {
				part.Tool.ImageURLs = append(part.Tool.ImageURLs, "/api/chats/"+chat.ID+"/tool-image?ref="+part.Tool.Ref+"&index="+strconv.Itoa(index))
			}
			part.Tool.SourceText = ""
			part.Text = ""
			part.Images = nil
		}
	}
	return chat
}
