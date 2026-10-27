package model

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

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
