package agent

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	chatmodel "spool/internal/model"
)

// Claude advertises the commands usable in print/SDK mode during initialize.
// Do not replace this with the terminal's slash menu: many terminal commands
// are unavailable through Spool's stream-json transport.
func claudeCommands(body map[string]any) []chatmodel.SlashCommand {
	commands := []chatmodel.SlashCommand{}
	entries, _ := body["commands"].([]any)
	seen := map[string]int{}
	for _, entry := range entries {
		value, _ := entry.(map[string]any)
		name := stringValue(value["name"])
		if !publicCommandName(name) {
			continue
		}
		description := stringValue(value["description"])
		if strings.HasPrefix(description, "(removed)") {
			continue
		}
		previous, duplicate := seen[name]
		// A native builtin wins over a custom command sharing its name,
		// regardless of the order in initialize.commands.
		if duplicate && (commands[previous].Builtin || value["builtin"] != true) {
			continue
		}
		command := chatmodel.SlashCommand{
			Name: name, Description: chatmodel.Clip(description, 2048),
			ArgumentHint: chatmodel.Clip(stringValue(value["argumentHint"]), 512),
			Builtin:      value["builtin"] == true,
			Kind:         claudeCommandKind(name),
			PluginID:     chatmodel.Clip(stringValue(value["plugin"]), 256),
		}
		if name == "code-review" {
			command.DisplayName = "Code review"
		}
		aliases, _ := value["aliases"].([]any)
		aliasSeen := map[string]bool{name: true}
		for _, raw := range aliases {
			alias := stringValue(raw)
			if publicCommandName(alias) && !aliasSeen[alias] {
				command.Aliases = append(command.Aliases, alias)
				aliasSeen[alias] = true
			}
		}
		if duplicate {
			commands[previous] = command
		} else {
			seen[name] = len(commands)
			commands = append(commands, command)
		}
	}
	// Plan mode is a native control-protocol operation in SDK mode, even
	// though /plan itself is not advertised as a print-mode slash command.
	if _, ok := seen["plan"]; !ok {
		commands = append(commands, chatmodel.SlashCommand{Name: "plan", Description: "Enter native plan mode", ArgumentHint: "[task]", Builtin: true, Kind: "control"})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}

func claudePrimaryCommand(name string) bool {
	switch name {
	case "code-review", "plan", "goal", "loop", "deep-research":
		return true
	default:
		return false
	}
}

// get_skills_dialog is optional on older CLIs. Missing skill metadata must not
// turn the remaining initialize.commands into skills. This read-only probe
// shares initialization's session, so advertised rows describe the same catalog.
func (p *agentProcess) discoverClaudeMenuCommands(ctx context.Context, initialization map[string]any) []chatmodel.SlashCommand {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	skills, _ := p.claudeControl(ctx, map[string]any{"subtype": "get_skills_dialog"})
	return claudeMenuCommands(initialization, skills)
}

func claudeMenuCommands(body, dialog map[string]any) []chatmodel.SlashCommand {
	skills := map[string]map[string]any{}
	rows, _ := dialog["skills"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["advertised"] != true {
			continue
		}
		switch row["state"] {
		case "on", "name-only", "user-invocable-only":
		default:
			continue
		}
		// display_name is the name used in the native slash-command catalog;
		// name may instead be a fully qualified settings key.
		name := stringValue(row["display_name"])
		if name == "" {
			name = stringValue(row["name"])
		}
		if publicCommandName(name) && skills[name] == nil {
			skills[name] = row
		}
	}
	commands := []chatmodel.SlashCommand{}
	for _, command := range claudeCommands(body) {
		if claudePrimaryCommand(command.Name) {
			commands = append(commands, command)
			continue
		}
		skill := skills[command.Name]
		if skill == nil || command.Kind != "prompt" {
			continue
		}
		command.Kind = "skill"
		command.DisplayName = chatmodel.Clip(stringValue(skill["display_name"]), 256)
		if description := stringValue(skill["description"]); description != "" {
			command.Description = chatmodel.Clip(description, 2048)
		}
		switch skill["source"] {
		case "user":
			command.Scope = "user"
		case "project":
			command.Scope = "repo"
		case "managed", "policy":
			command.Scope = "admin"
		case "built-in":
			command.Scope = "system"
		}
		commands = append(commands, command)
	}
	return commands
}

func claudeCommandKind(name string) string {
	switch name {
	case "clear", "rename":
		return "client"
	case "plan", "goal", "model", "autocompact", "color", "compact", "config", "output-style", "context", "effort", "fast", "focus", "heapdump", "mcp", "reload-plugins", "reload-skills", "usage", "list-agents":
		return "control"
	default:
		return "prompt"
	}
}

func IsControlCommand(backend chatmodel.Backend, name string) bool {
	if backend == chatmodel.BackendClaude {
		switch name {
		case "cost", "stats", "settings", "peers":
			return true
		}
		return name == "plan" || claudeCommandKind(name) == "control"
	}
	command, ok := chatmodel.FindSlashCommand(codexCommands(), name)
	return ok && command.Kind == "control"
}

func publicCommandName(name string) bool {
	return name != "" && name != "workflow-launch-exec" && len(name) <= 200 && !strings.HasPrefix(name, "__") && strings.IndexFunc(name, func(ch rune) bool {
		return unicode.IsSpace(ch) || unicode.IsControl(ch) || ch == '/' || ch == '\\'
	}) < 0
}

// app-server exposes RPC methods rather than a discoverable slash-command
// registry. This catalog maps public CLI command names to native protocol
// capabilities; it is deliberately labelled "protocol", not live discovery.
// Terminal-only presentation commands have no equivalent on this transport.
// Skills are discovered separately in the actual project through skills/list.
func codexCommands() []chatmodel.SlashCommand {
	definitions := []struct {
		name, description, arguments string
	}{
		{"goal", "Set a native agent goal", "[condition|clear]"},
		{"plan", "Use native plan mode", "[task]"},
		{"review", "Review changes in the working tree or a branch", "[base-branch]"},
	}
	commands := make([]chatmodel.SlashCommand, 0, len(definitions))
	for _, definition := range definitions {
		command := chatmodel.SlashCommand{Name: definition.name, Description: definition.description, ArgumentHint: definition.arguments, Builtin: true, Kind: "control"}
		switch command.Name {
		case "goal", "review":
			command.Kind = "prompt"
		}
		if command.Name == "review" {
			command.DisplayName = "Code review"
		}
		commands = append(commands, command)
	}
	return commands
}
