package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseGoalCommand(t *testing.T) {
	for _, tt := range []struct {
		message string
		want    *GoalCommand
	}{
		{"/goal", &GoalCommand{Action: "get"}},
		{" \n/goal \t", &GoalCommand{Action: "get"}},
		{"/goal clear", &GoalCommand{Action: "clear"}},
		{"/goal stop", &GoalCommand{Action: "clear"}},
		{"/goal off", &GoalCommand{Action: "clear"}},
		{"/goal reset", &GoalCommand{Action: "clear"}},
		{"/goal none", &GoalCommand{Action: "clear"}},
		{"/goal cancel", &GoalCommand{Action: "clear"}},
		{"/goal fix tests\nand verify", &GoalCommand{Action: "set", Objective: "fix tests\nand verify"}},
		{"/goal\u3000修好测试", &GoalCommand{Action: "set", Objective: "修好测试"}},
		{"/goal clear the tests", &GoalCommand{Action: "set", Objective: "clear the tests"}},
		{"/goal/path", nil}, {"/goals", nil}, {"/Goal", nil},
		{"explain /goal", nil}, {"`/goal`", nil}, {"/", nil}, {"", nil},
	} {
		t.Run(tt.message, func(t *testing.T) {
			got, err := ParseGoalCommand(tt.message)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseGoalCommand(%q) = %#v, %v; want %#v", tt.message, got, err, tt.want)
			}
		})
	}
}

func TestGoalCommandCharacterLimit(t *testing.T) {
	if _, err := ParseGoalCommand("/goal " + strings.Repeat("目", 4000)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGoalCommand("/goal " + strings.Repeat("目", 4001)); err == nil {
		t.Fatal("accepted oversized goal")
	}
}
