package model

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SlashCommand describes an agent command and its execution route. Native
// descriptions remain provider-owned.
type SlashCommand struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"displayName,omitempty"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Builtin      bool     `json:"builtin"`
	Kind         string   `json:"kind"` // control, prompt, client, or skill
	Scope        string   `json:"scope,omitempty"`
	PluginID     string   `json:"pluginId,omitempty"`
}

type SlashCommandInput struct {
	Name, Arguments string
}

// ParseSlashCommand leaves file paths, quoted commands and ordinary prose alone.
func ParseSlashCommand(message string) *SlashCommandInput {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "/") {
		return nil
	}
	name, arguments := message[1:], ""
	if index := strings.IndexFunc(name, unicode.IsSpace); index >= 0 {
		name, arguments = name[:index], strings.TrimSpace(name[index:])
	}
	if name == "" || strings.ContainsAny(name, "/\\`\"") {
		return nil
	}
	return &SlashCommandInput{Name: name, Arguments: arguments}
}

func FindSlashCommand(commands []SlashCommand, name string) (SlashCommand, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	for _, command := range commands {
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return SlashCommand{}, false
}

type GoalCommand struct {
	Action    string
	Objective string
}

// Only a leading, exact /goal token is a command; paths and quoted text are not.
func ParseGoalCommand(message string) (*GoalCommand, error) {
	message = strings.TrimSpace(message)
	name, argument := message, ""
	if index := strings.IndexFunc(message, unicode.IsSpace); index >= 0 {
		name, argument = message[:index], strings.TrimSpace(message[index:])
	}
	if name != "/goal" {
		return nil, nil
	}
	if argument == "" {
		return &GoalCommand{Action: "get"}, nil
	}
	switch argument {
	case "clear", "stop", "off", "reset", "none", "cancel":
		return &GoalCommand{Action: "clear"}, nil
	}
	if utf8.RuneCountInString(argument) > 4000 {
		return nil, fmt.Errorf("Goal conditions must be at most 4000 characters")
	}
	return &GoalCommand{Action: "set", Objective: argument}, nil
}
