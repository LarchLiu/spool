package agent

import (
	"fmt"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// A spawn returning successfully does not mean the child finished. Each task
// has its own lifecycle, and emitted snapshots must remain immutable.
type liveTask struct {
	part        chatmodel.Part
	progress    chatmodel.TaskProgress
	description string
	status      string
	duration    int64
	last        chatmodel.Part
	seenTools   map[string]bool
	revision    uint64
}

func newLiveTask(id, name string) *liveTask {
	if strings.TrimSpace(name) == "" {
		name = "Subagent"
	}
	return &liveTask{part: chatmodel.Part{ID: id, Role: "assistant", Kind: "tool_use", Name: chatmodel.Clip(name, 240)}, status: chatmodel.ToolRunning}
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

func (task *liveTask) event() (AgentEvent, bool) {
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
	part := task.part
	part.Text = strings.Join(lines, "\n")
	part.Native = &chatmodel.ToolNative{Kind: "task", Status: task.status, DurationMs: task.duration, Task: &progress}
	if task.last.Native != nil && task.last.Text == part.Text && task.last.Name == part.Name && task.last.Native.Status == task.status && *task.last.Native.Task == progress {
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
