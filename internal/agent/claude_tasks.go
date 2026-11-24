package agent

import (
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

const (
	// A child of a finished turn that reports nothing for this long is given
	// up on. Tool calls and summaries report well within it.
	claudeTaskSilence = 15 * time.Minute
	// How long the turn answering a task notification has to show itself.
	claudeFollowupGrace = 15 * time.Second
)

// Tool calls and task lifecycle notifications alias the same live task. No
// command names are involved, and nested transcripts never become chat text.
type claudeTasks struct {
	tasks map[string]*liveTask
	tools map[string]*liveTask
	order []*liveTask
	// When any task last reported; see abandon.
	heard time.Time
}

func (tasks *claudeTasks) create(id, title string) *liveTask {
	if tasks.tasks == nil {
		tasks.tasks, tasks.tools = map[string]*liveTask{}, map[string]*liveTask{}
	}
	task := newLiveTask("claude-task:"+id, title)
	task.progress.ID = id
	tasks.tasks[id] = task
	tasks.order = append(tasks.order, task)
	return task
}

func (tasks *claudeTasks) running() bool {
	for _, task := range tasks.order {
		if task.running() {
			return true
		}
	}
	return false
}

// Claude Code offers no way to read a child's state, so a lifecycle that
// stopped reporting cannot hold its request open for ever. Nothing said the
// work went wrong: its outcome is unknown, not failed.
func (tasks *claudeTasks) abandon(emit func(AgentEvent) error) error {
	for _, task := range tasks.order {
		if task.running() {
			task.status, task.progress.Summary = "unknown", "Subagent stopped reporting its progress."
			if err := emitTask(task, emit); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tasks *claudeTasks) system(value map[string]any, emit func(AgentEvent) error) (bool, error) {
	subtype, id := stringValue(value["subtype"]), stringValue(value["task_id"])
	if id == "" || (subtype != "task_started" && subtype != "task_progress" && subtype != "task_notification") {
		return false, nil
	}
	task := tasks.tasks[id]
	if task == nil {
		task = tasks.tools[stringValue(value["tool_use_id"])]
	}
	if task == nil {
		// Only its start says what a task is. Background shell commands report
		// the same lifecycle, and so does an agent an earlier request gave up
		// waiting for: neither is a subagent of this one.
		kind := stringValue(value["task_type"])
		if subtype != "task_started" || kind != "" && kind != "local_agent" && kind != "agent" {
			return true, nil
		}
		task = tasks.create(id, stringValue(value["description"]))
	}
	tasks.heard = time.Now()
	tasks.tasks[id] = task
	if tool := stringValue(value["tool_use_id"]); tool != "" {
		tasks.tools[tool] = task
	}
	task.progress.ID = id
	if !task.running() {
		return true, nil
	}
	if title := strings.TrimSpace(stringValue(value["description"])); title != "" {
		task.part.Name = chatmodel.Clip(title, 240)
	}
	if parent := stringValue(value["parent_task_id"]); parent != "" {
		task.progress.ParentID = parent
	}
	if background, ok := value["is_backgrounded"].(bool); ok {
		task.progress.Background = background
	}
	if subtype == "task_notification" {
		switch value["status"] {
		case "completed":
			task.status = chatmodel.ToolCompleted
		case "failed":
			task.status = chatmodel.ToolFailed
		case "stopped":
			task.status, task.progress.Summary = "stopped", "Task stopped."
		default:
			return true, nil
		}
	}
	// Claude Code may report a child only through these events, without
	// forwarding what it did. Its steps are then what the events tell: each
	// progress summary, and the tool of each newly counted call.
	told := len(task.stepIDs) == 0 && subtype == "task_progress"
	if summary := strings.TrimSpace(stringValue(value["summary"])); summary != "" && summary != task.part.Name && value["status"] != "stopped" {
		task.progress.Summary = chatmodel.Clip(summary, 4000)
		if told {
			task.step(chatmodel.TaskStep{Kind: chatmodel.TaskStepMessage, Text: summary})
		}
	}
	tool, calls := stringValue(value["last_tool_name"]), task.progress.ToolUses
	if tool != "" {
		task.progress.LastTool = chatmodel.Clip(tool, 200)
	}
	if usage, ok := value["usage"].(map[string]any); ok {
		if n, ok := numberValue(usage["tool_uses"]); ok && n >= 0 {
			task.progress.ToolUses = n
		}
		if n, ok := numberValue(usage["total_tokens"]); ok && n >= 0 {
			task.progress.Tokens = n
		}
		if n, ok := numberValue(usage["duration_ms"]); ok && n >= 0 {
			task.duration = int64(n)
		}
	}
	if told && tool != "" && task.progress.ToolUses > calls {
		task.step(chatmodel.TaskStep{Name: tool})
	}
	return true, emitTask(task, emit)
}

func (tasks *claudeTasks) call(item map[string]any, parent string, emit func(AgentEvent) error) (bool, error) {
	id, name := stringValue(item["id"]), strings.ToLower(stringValue(item["name"]))
	if id == "" || (name != "agent" && name != "task") {
		return false, nil
	}
	task := tasks.tools[id]
	if task != nil {
		return true, nil
	}
	tasks.heard = time.Now()
	input, _ := item["input"].(map[string]any)
	task = tasks.create(id, stringValue(input["description"]))
	tasks.tools[id] = task
	task.description = chatmodel.Clip(strings.TrimSpace(stringValue(input["prompt"])), 2000)
	task.progress.AgentType = chatmodel.Clip(stringValue(input["subagent_type"]), 200)
	task.progress.Background = input["run_in_background"] == true
	if p := tasks.tools[parent]; p != nil {
		task.progress.ParentID = p.progress.ID
	} else {
		task.progress.ParentID = parent
	}
	return true, emitTask(task, emit)
}

func (tasks *claudeTasks) result(item, record map[string]any, emit func(AgentEvent) error) (bool, error) {
	task := tasks.tools[stringValue(item["tool_use_id"])]
	if task == nil {
		return false, nil
	}
	tasks.heard = time.Now()
	if !task.running() {
		return true, nil
	}
	body, _ := record["toolUseResult"].(map[string]any)
	if id := stringValue(itemField(body, "agentId", "task_id")); id != "" {
		tasks.tasks[id] = task
		task.progress.ID = id
	}
	if body["isAsync"] == true || body["status"] == "async_launched" {
		task.progress.Background = true
	}
	if item["is_error"] == true {
		task.status = chatmodel.ToolFailed
	} else if !task.progress.Background {
		task.status = chatmodel.ToolCompleted
	}
	if !task.running() {
		task.progress.Summary = chatmodel.Clip(strings.TrimSpace(claudeToolResultPart(item).Text), 4000)
	}
	if n, ok := numberValue(body["totalDurationMs"]); ok {
		task.duration = int64(n)
	}
	return true, emitTask(task, emit)
}

func (tasks *claudeTasks) nested(value map[string]any, parent string, emit func(AgentEvent) error) error {
	// Foreground slash-command forks have no spawning tool call. Their
	// lifecycle uses task_id, while forwarded messages carry that ID as
	// agent_id beside a synthetic parent_tool_use_id.
	if task := tasks.tasks[stringValue(value["agent_id"])]; task != nil {
		tasks.tools[parent] = task
	}
	if tasks.tools[parent] != nil {
		tasks.heard = time.Now()
	}
	message, _ := value["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, block := range blocks {
		item, _ := block.(map[string]any)
		if value["type"] == "assistant" && item["type"] == "tool_use" {
			if handled, err := tasks.call(item, parent, emit); handled || err != nil {
				if err != nil {
					return err
				}
				continue
			}
			if task := tasks.tools[parent]; task != nil {
				id, name := stringValue(item["id"]), stringValue(item["name"])
				task.tool(id, name)
				if input, _ := item["input"].(map[string]any); name != "" {
					step := chatmodel.TaskStep{ID: id, Name: name, Input: taskStepInput(input)}
					// Only a call that names itself can later be told to have ended.
					if id != "" {
						step.Status = chatmodel.ToolRunning
					}
					task.step(step)
				}
			}
		}
		if value["type"] == "user" && item["type"] == "tool_result" {
			if _, err := tasks.result(item, value, emit); err != nil {
				return err
			}
			if task := tasks.tools[parent]; task != nil {
				status := chatmodel.ToolCompleted
				if item["is_error"] == true {
					status = chatmodel.ToolFailed
				}
				task.settle(stringValue(item["tool_use_id"]), status)
			}
		}
		if task := tasks.tools[parent]; task != nil && task.running() {
			if text := strings.TrimSpace(stringValue(item["text"])); value["type"] == "assistant" && item["type"] == "text" {
				task.progress.Summary = chatmodel.Clip(text, 4000)
				if text != "" {
					task.step(chatmodel.TaskStep{Kind: chatmodel.TaskStepMessage, Text: text})
				}
			}
			if err := emitTask(task, emit); err != nil {
				return err
			}
		}
	}
	return nil
}
