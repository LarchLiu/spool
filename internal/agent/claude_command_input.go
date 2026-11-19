package agent

import (
	"strings"

	chatmodel "spool/internal/model"
)

// Claude records a user-invoked skill as a command envelope, then adds its
// instructions in a separate isMeta record. Preserve the invocation as input
// for both turn indexing and display; the instructions remain hidden. Require
// both identifying fields so local-command output and partial echoes stay hidden.
// Claude omits command-args when a skill is invoked without arguments.
func claudeCommandInput(content any) string {
	if blocks, ok := content.([]any); ok {
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if block["type"] != "text" {
				return ""
			}
		}
	}
	texts, _ := userContentTexts(content)
	remaining := strings.TrimSpace(strings.Join(texts, "\n"))
	fields := map[string]string{}
	for remaining != "" && len(fields) < 3 {
		found := false
		for _, name := range []string{"command-message", "command-name", "command-args"} {
			rest, ok := strings.CutPrefix(remaining, "<"+name+">")
			if !ok {
				continue
			}
			value, rest, closed := strings.Cut(rest, "</"+name+">")
			if _, duplicate := fields[name]; !closed || duplicate {
				return ""
			}
			fields[name], remaining = value, strings.TrimSpace(rest)
			found = true
			break
		}
		if !found {
			return ""
		}
	}
	name := strings.TrimSpace(fields["command-name"])
	command := chatmodel.ParseSlashCommand(name)
	if remaining != "" || strings.TrimSpace(fields["command-message"]) == "" || command == nil || name != "/"+command.Name {
		return ""
	}
	// Local controls (for example /compact) are native status records. They
	// have no model response and must not split or expose compaction metadata.
	if claudeCommandKind(command.Name) != "prompt" && !claudePrimaryCommand(command.Name) {
		return ""
	}
	if arguments := strings.TrimSpace(fields["command-args"]); arguments != "" {
		return name + " " + arguments
	}
	return name
}
