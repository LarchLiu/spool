package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

const (
	// How many of a child's latest steps its row keeps.
	taskStepLimit = 20
	// A step is a line of a list: its text and input are bounded to match.
	taskStepBytes = 512
)

// A spawn returning successfully does not mean the child finished. Each task
// has its own lifecycle, and emitted snapshots must remain immutable.
type liveTask struct {
	part        chatmodel.Part
	progress    chatmodel.TaskProgress
	description string
	status      string
	duration    int64
	// The duration is this host's own measure: the provider gave none.
	measured  bool
	steps     []chatmodel.TaskStep
	stepIDs   map[string]bool
	last      chatmodel.Part
	seenTools map[string]bool
	revision  uint64
}

func newLiveTask(id, name string) *liveTask {
	if strings.TrimSpace(name) == "" {
		name = "Subagent"
	}
	task := &liveTask{part: chatmodel.Part{ID: id, Role: "assistant", Kind: "tool_use", Name: chatmodel.Clip(name, 240)}, status: chatmodel.ToolRunning}
	task.progress.StartedAt = time.Now().UnixMilli()
	return task
}

func (task *liveTask) running() bool { return task.status == chatmodel.ToolRunning }

func (task *liveTask) tool(id, name string) {
	if name == "" {
		return
	}
	task.progress.LastTool = chatmodel.Clip(name, 200)
	if id != "" {
		if task.seenTools == nil {
			task.seenTools = map[string]bool{}
		}
		if !task.seenTools[id] {
			task.seenTools[id] = true
			task.progress.ToolUses++
		}
	}
}

// step records a tool call or a message among the task's latest steps. A step
// that names itself is updated in place when it is told again, and is not
// brought back once newer steps have pushed it out.
func (task *liveTask) step(step chatmodel.TaskStep) {
	step.Name, step.Text = chatmodel.Clip(step.Name, 200), chatmodel.Clip(step.Text, taskStepBytes)
	message := step.Kind == chatmodel.TaskStepMessage
	if step.ID != "" {
		for i := range task.steps {
			current := &task.steps[i]
			if current.ID != step.ID {
				continue
			}
			if step.Status != "" {
				current.Status = step.Status
			}
			if step.Input != "" {
				current.Input = step.Input
			}
			if step.Text != "" {
				current.Text = step.Text
			}
			if step.Kind != "" {
				current.Kind, current.Path, current.Query = step.Kind, step.Path, step.Query
			}
			return
		}
		if task.stepIDs[step.ID] {
			return
		}
		if task.stepIDs == nil {
			task.stepIDs = map[string]bool{}
		}
		task.stepIDs[step.ID] = true
	}
	if message {
		// A summary is told again with every report until the child says more.
		for _, previous := range slices.Backward(task.steps) {
			if previous.Kind == chatmodel.TaskStepMessage {
				if previous.Text == step.Text {
					return
				}
				break
			}
		}
	} else if last := len(task.steps) - 1; last >= 0 && step.ID != "" {
		// A call first told by its tool's name alone is now told in full.
		if previous := task.steps[last]; previous.ID == "" && previous.Kind != chatmodel.TaskStepMessage && previous.Name == step.Name {
			task.steps[last] = step
			return
		}
	}
	task.steps = append(task.steps, step)
	if extra := len(task.steps) - taskStepLimit; extra > 0 {
		task.steps = task.steps[extra:]
	}
}

// settle records how a call among the latest steps ended.
func (task *liveTask) settle(id, status string) {
	for i := range task.steps {
		if id != "" && task.steps[i].ID == id {
			task.steps[i].Status = status
		}
	}
}

// taskStepInput keeps what a step's label can be read from: the scalar fields
// of a tool's input. File bodies and other long values say nothing it needs.
func taskStepInput(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	kept := map[string]any{}
	for _, key := range keys {
		if len(kept) == 16 {
			break
		}
		switch value := input[key].(type) {
		case string:
			if key == "command" || key == "cmd" {
				kept[key] = chatmodel.Clip(value, taskStepBytes)
			} else if len(value) <= taskStepBytes {
				kept[key] = value
			}
		case bool, float64:
			kept[key] = value
		}
	}
	if len(kept) == 0 {
		return ""
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func sameTaskProgress(a, b chatmodel.TaskProgress) bool {
	if !slices.Equal(a.Steps, b.Steps) {
		return false
	}
	a.Steps, b.Steps = nil, nil
	return reflect.DeepEqual(a, b)
}

func (task *liveTask) event() (AgentEvent, bool) {
	// A child given more work is timed from then, not from its earlier run.
	if task.running() && task.measured {
		task.duration, task.measured = 0, false
		task.progress.StartedAt = time.Now().UnixMilli()
	}
	// Nothing is known of a child that stopped reporting, least of all when it ended.
	if !task.running() && task.status != "unknown" && task.duration == 0 && task.progress.StartedAt > 0 {
		task.duration, task.measured = max(1, time.Now().UnixMilli()-task.progress.StartedAt), true
	}
	lines := []string{task.part.Name}
	if task.description != "" && task.description != task.part.Name {
		lines = append(lines, task.description)
	}
	if task.progress.Summary != "" {
		lines = append(lines, task.progress.Summary)
	}
	if task.progress.ParentID != "" {
		lines = append(lines, "Parent: "+task.progress.ParentID)
	}
	if task.progress.AgentType != "" {
		lines = append(lines, "Agent: "+task.progress.AgentType)
	}
	if task.progress.Background {
		lines = append(lines, "Background task")
	}
	if task.progress.LastTool != "" {
		lines = append(lines, "Tool: "+task.progress.LastTool)
	}
	if task.progress.ToolUses > 0 {
		lines = append(lines, fmt.Sprintf("Tool uses: %d", task.progress.ToolUses))
	}
	if task.progress.Tokens > 0 {
		lines = append(lines, fmt.Sprintf("Tokens: %d", task.progress.Tokens))
	}
	if task.duration > 0 {
		lines = append(lines, "Duration: "+(time.Duration(task.duration)*time.Millisecond).String())
	}
	progress := task.progress
	if task.description != task.part.Name {
		progress.Prompt = task.description
	}
	// The window keeps moving; a snapshot keeps the steps it was emitted with.
	progress.Steps = slices.Clone(task.steps)
	part := task.part
	part.Text = strings.Join(lines, "\n")
	part.Native = &chatmodel.ToolNative{Kind: "task", Status: task.status, DurationMs: task.duration, Task: &progress}
	if task.last.Native != nil && task.last.Text == part.Text && task.last.Name == part.Name && task.last.Native.Status == task.status && sameTaskProgress(*task.last.Native.Task, progress) {
		return AgentEvent{}, false
	}
	kind := "tool"
	if task.last.Native == nil {
		kind = "activity"
	}
	task.last = part
	task.revision++
	return AgentEvent{Type: kind, Text: "Subagent: " + part.Name, Part: &part}, true
}

func emitTask(task *liveTask, emit func(AgentEvent) error) error {
	if event, ok := task.event(); ok {
		return emit(event)
	}
	return nil
}
