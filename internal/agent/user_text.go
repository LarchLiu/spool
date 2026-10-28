package agent

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	chatmodel "spool/internal/model"
)

// This projection is display-only. In particular, attachment-only input still
// owns a native turn even though it has no text to render.
type userTextProjection struct {
	Text           string
	HasAttachments bool
}

func (p userTextProjection) genuine() bool {
	return p.HasAttachments || strings.TrimSpace(p.Text) != ""
}

func cleanUserText(text string, hasImages bool) userTextProjection {
	result := userTextProjection{Text: text}
	for {
		before := result.Text
		lines := userMarkdownLines(before)
		if prefix, ok := attachedFilesPrefix(before, lines); ok {
			result.Text, result.HasAttachments = prefix, true
		}
		result.Text = stripUserContext(result.Text)
		if hasImages {
			result.Text = stripImageLabels(result.Text)
		}
		if result.Text == before {
			return result
		}
	}
}

func cleanUserTextBlocks(texts []string, hasImages bool) userTextProjection {
	text := strings.Join(texts, "\n")
	if len(texts) < 2 {
		return cleanUserText(text, hasImages)
	}
	// Only discard complete native blocks, using the whole message's Markdown
	// state so a literal example spanning several text blocks remains opaque.
	lines := userMarkdownLines(text)
	kept := make([]string, 0, len(texts))
	leadingContext, hasBody := false, false
	at := 0
	for _, block := range texts {
		start := at + len(block) - len(strings.TrimLeft(block, " \t\r\n"))
		at += len(block) + 1
		if userMarkdownOutside(text, lines, start) && block != "" && stripUserContext(block) == "" {
			leadingContext = leadingContext || !hasBody
			continue
		}
		kept = append(kept, block)
		hasBody = hasBody || strings.TrimSpace(block) != ""
	}
	text = strings.Join(kept, "\n")
	if leadingContext {
		text = stripUserContextAfter(text, true)
	}
	return cleanUserText(text, hasImages)
}

func attachedFilesPrefix(text string, lines []userMarkdownLine) (string, bool) {
	for _, line := range lines {
		if !line.plain || line.inline != 0 || text[line.start:line.end] != attachedFilesMarker {
			continue
		}
		start := line.start
		if start > 0 && !strings.HasSuffix(text[:start], "\n\n") {
			continue
		}
		entries := text[line.next:]
		if entries == "" {
			continue
		}
		valid := true
		// Native image labels can follow the list in separate text blocks.
		// Removing those labels leaves only their separating blank lines.
		for _, entry := range strings.Split(strings.TrimRight(entries, "\n"), "\n") {
			if !validAttachedFileEntry(entry) {
				valid = false
				break
			}
		}
		if valid {
			if start > 0 {
				start -= 2 // Only remove the separator added by agentTextInput.
			}
			return text[:start], true
		}
	}
	return text, false
}

func validAttachedFileEntry(line string) bool {
	if !strings.HasPrefix(line, "- \"") {
		return false
	}
	name, err := strconv.QuotedPrefix(line[2:])
	if err != nil || !strings.HasPrefix(line[2+len(name):], ": \"") {
		return false
	}
	quotedPath := line[2+len(name)+2:]
	filename, nameErr := strconv.Unquote(name)
	value, pathErr := strconv.Unquote(quotedPath)
	// Require exactly the generator's Go-quoted strings, not arbitrary bullets,
	// Markdown links, JSON objects or a truncated list followed by a request.
	normalPath := strings.ReplaceAll(value, "\\", "/")
	absolute := strings.HasPrefix(normalPath, "/") || (len(normalPath) > 3 &&
		((normalPath[0] >= 'a' && normalPath[0] <= 'z') || (normalPath[0] >= 'A' && normalPath[0] <= 'Z')) && normalPath[1:3] == ":/")
	return nameErr == nil && pathErr == nil && filename != "" && absolute &&
		!strings.ContainsAny(filename, "/\\\r\n") && path.Base(normalPath) == filename &&
		strconv.Quote(filename) == name && strconv.Quote(value) == quotedPath
}

var nativeUserTags = map[string]bool{
	"environment_context": true, "permissions instructions": true,
	"system-reminder": true, "app-context": true, "skills_instructions": true,
	"collaboration_mode": true, "multi_agent_mode": true, "tools": true,
	"codex_apps_client_time_context": true, "user_instructions": true,
	"turn_aborted": true, "image_resize_notice": true, "user_shell_command": true,
	"bash-input": true, "bash-stdout": true, "bash-stderr": true, "user-memory-input": true,
	"command-name": true, "command-message": true, "command-args": true,
	"local-command-stdout": true, "local-command-stderr": true, "local-command-caveat": true,
}

func stripUserContext(text string) string {
	return stripUserContextAfter(text, false)
}

func stripUserContextAfter(text string, recognized bool) string {
	remaining := text
	for {
		start := len(remaining) - len(strings.TrimLeft(remaining, " \t\r\n"))
		lines := userMarkdownLines(remaining)
		if !userMarkdownOutside(remaining, lines, start) {
			break
		}
		end := nativeUserBlockEnd(remaining, lines, start)
		if end == start {
			break
		}
		remaining, recognized = remaining[end:], true
	}
	// The IDE extension's Markdown preamble has no closing tag. Only a known
	// context header before an out-of-code request heading can delimit it.
	lines := userMarkdownLines(remaining)
	trimmed := strings.TrimLeft(remaining, " \t\r\n")
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	ideContext := userMarkdownOutside(remaining, lines, len(remaining)-len(trimmed)) &&
		(firstLine == "# Context from my IDE setup:" || firstLine == "# Files mentioned by the user:")
	if ideContext || (recognized && requestHeading(firstLine)) {
		for _, line := range lines {
			if line.plain && line.inline == 0 && requestHeading(remaining[line.start:line.end]) {
				return strings.TrimLeft(remaining[line.next:], "\r\n")
			}
		}
	}
	if recognized {
		if strings.TrimSpace(remaining) == "" {
			return ""
		}
		return strings.TrimLeft(remaining, "\r\n")
	}
	return text
}

func requestHeading(line string) bool {
	return line == "## My request for Codex:" || line == "## My request:"
}

func nativeUserBlockEnd(text string, lines []userMarkdownLine, start int) int {
	rest := text[start:]
	if strings.HasPrefix(rest, "# AGENTS.md instructions for ") {
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			body := newline + len(rest[newline:]) - len(strings.TrimLeft(rest[newline:], "\r\n"))
			if strings.HasPrefix(rest[body:], "<INSTRUCTIONS>") {
				if end := completeUserTagEnd(text, lines, start+body, "INSTRUCTIONS"); end > start+body {
					return end
				}
			}
		}
	}
	if end := userInterruptionEnd(rest); end > 0 {
		return start + end
	}
	const patchWarning = "Warning: apply_patch was requested via exec_command. Use the apply_patch tool instead of exec_command."
	if strings.HasPrefix(rest, patchWarning) && (len(rest) == len(patchWarning) || rest[len(patchWarning)] == '\n' || rest[len(patchWarning)] == '\r') {
		return start + len(patchWarning)
	}
	const caveat = "Caveat: The messages below were generated by the user while running local commands. DO NOT respond to these messages or otherwise consider them in your response unless the user explicitly asks you to."
	if strings.HasPrefix(rest, caveat) {
		next := start + len(caveat)
		next += len(text[next:]) - len(strings.TrimLeft(text[next:], " \t\r\n"))
		if userMarkdownOutside(text, lines, next) && (strings.HasPrefix(text[next:], "<command-") || strings.HasPrefix(text[next:], "<local-command-")) {
			if end := nativeUserBlockEnd(text, lines, next); end > next {
				return end
			}
		}
	}
	const summary = "This session is being continued from a previous conversation that ran out of context."
	if strings.HasPrefix(rest, summary) {
		for _, line := range lines {
			if line.start <= start || !line.plain || line.inline != 0 {
				continue
			}
			if text[line.start:line.end] == "Summary:" {
				return len(text)
			}
			if text[line.start:line.end] == "<summary>" {
				if end := completeUserTagEnd(text, lines, line.start, "summary"); end > line.start {
					return end
				}
			}
		}
	}
	if !strings.HasPrefix(rest, "<") {
		return start
	}
	if end := strings.IndexByte(rest, '>'); end > 1 {
		tag := rest[1:end]
		if nativeUserTags[tag] || (strings.HasPrefix(tag, "ide_") && validIDETag(tag)) {
			return completeUserTagEnd(text, lines, start, tag)
		}
	}
	return start
}

func userInterruptionEnd(text string) int {
	for _, notice := range []string{"[Request interrupted by user]", "[Request interrupted by user for tool use]"} {
		if strings.HasPrefix(text, notice) && (len(text) == len(notice) || text[len(notice)] == '\n' || text[len(notice)] == '\r') {
			return len(notice)
		}
	}
	return 0
}

func validIDETag(tag string) bool {
	for _, ch := range tag {
		if ch != '_' && (ch < 'a' || ch > 'z') {
			return false
		}
	}
	return len(tag) > len("ide_")
}

func completeUserTagEnd(text string, lines []userMarkdownLine, start int, tag string) int {
	open, close := "<"+tag+">", "</"+tag+">"
	depth := 1
	for at := start + len(open); at < len(text); {
		index := strings.IndexByte(text[at:], '<')
		if index < 0 {
			break
		}
		at += index
		if userMarkdownOutside(text, lines, at) {
			if strings.HasPrefix(text[at:], open) {
				depth++
			} else if strings.HasPrefix(text[at:], close) {
				depth--
				if depth == 0 {
					return at + len(close)
				}
			}
		}
		at++
	}
	return start
}

var imageLabel = regexp.MustCompile(`^<image(?:[ \t]+[^<>]+)?>$|^</image>$`)

func stripImageLabels(text string) string {
	var out strings.Builder
	start := 0
	for _, line := range userMarkdownLines(text) {
		if line.plain && line.inline == 0 && imageLabel.MatchString(text[line.start:line.end]) {
			out.WriteString(text[start:line.start])
			start = line.next
		}
	}
	if start == 0 {
		return text
	}
	out.WriteString(text[start:])
	return out.String()
}

// Keep Markdown examples opaque without reformatting the user's text. Offsets
// let block delimiters on the same line work as well as multiline wrappers.
type userMarkdownLine struct {
	start, end, next int
	plain            bool
	inline           int
}

func userMarkdownLines(text string) []userMarkdownLine {
	var lines []userMarkdownLine
	var fence byte
	fenceSize, inline := 0, 0
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		next := len(text)
		if end < 0 {
			end = len(text)
		} else {
			end += start
			next = end + 1
		}
		line := strings.TrimSuffix(text[start:end], "\r")
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		entry := userMarkdownLine{start: start, end: start + len(line), next: next, plain: true, inline: inline}
		if fence != 0 {
			entry.plain = false
			run := len(trimmed) - len(strings.TrimLeft(trimmed, string(fence)))
			if indent <= 3 && run >= fenceSize && strings.Trim(trimmed[run:], " \t") == "" {
				fence, fenceSize = 0, 0
			}
		} else if indent >= 4 || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "\t") {
			entry.plain = false
		} else if inline == 0 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			fence = trimmed[0]
			fenceSize = len(trimmed) - len(strings.TrimLeft(trimmed, string(fence)))
			entry.plain = false
		} else {
			inline = userInlineState(line, inline)
		}
		lines = append(lines, entry)
		start = next
	}
	return lines
}

func userInlineState(text string, inline int) int {
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && inline == 0 && i+1 < len(text) {
			i++
		} else if text[i] == '`' {
			n := 1
			for i+1 < len(text) && text[i+1] == '`' {
				n++
				i++
			}
			if inline == 0 {
				inline = n
			} else if inline == n {
				inline = 0
			}
		}
	}
	return inline
}

func userMarkdownOutside(text string, lines []userMarkdownLine, at int) bool {
	i := sort.Search(len(lines), func(i int) bool { return lines[i].next > at })
	return i < len(lines) && lines[i].plain && userInlineState(text[lines[i].start:at], lines[i].inline) == 0
}

func userContentTexts(content any) ([]string, bool) {
	if text, ok := content.(string); ok {
		return []string{text}, false
	}
	blocks, _ := content.([]any)
	var texts []string
	images := false
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		switch stringValue(block["type"]) {
		case "text", "input_text":
			if text := stringValue(block["text"]); text != "" {
				texts = append(texts, text)
			}
		case "image", "input_image":
			images = true
		}
	}
	return texts, images
}

func projectUserParts(parts []chatmodel.Part) []chatmodel.Part {
	var texts []string
	images := false
	for _, part := range parts {
		if part.Role == "user" {
			if part.Kind == "text" {
				texts = append(texts, part.Text)
			}
			images = images || part.Kind == "image"
		}
	}
	text := strings.Join(texts, "\n")
	clean := cleanUserTextBlocks(texts, images)
	if clean.Text == text {
		return parts
	}
	out := make([]chatmodel.Part, 0, len(parts))
	emitted := false
	for _, part := range parts {
		if part.Role == "user" && part.Kind == "text" {
			if !emitted && clean.Text != "" {
				part.Text = clean.Text
				out = append(out, part)
			}
			emitted = true
		} else {
			out = append(out, part)
		}
	}
	return out
}
