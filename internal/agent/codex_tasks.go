package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

type codexTaskRead struct {
	task     *liveTask
	method   string
	sent     time.Time
	revision uint64
}

type codexTaskCall struct{ thread, id, receiver string }

type codexTasks struct {
	root     string
	tasks    map[string]*liveTask
	order    []*liveTask
	reads    map[string]codexTaskRead
	next     map[string]time.Time
	failures map[string]int
	turns    map[string]string
	calls    map[codexTaskCall]bool
}

func (tasks *codexTasks) ensure(id, parent, title string) *liveTask {
	if id == "" || id == tasks.root {
		return nil
	}
	if tasks.tasks == nil {
		tasks.tasks = map[string]*liveTask{}
		tasks.reads = map[string]codexTaskRead{}
		tasks.next = map[string]time.Time{}
		tasks.failures = map[string]int{}
		tasks.turns = map[string]string{}
	}
	if task := tasks.tasks[id]; task != nil {
		return task
	}
	task := newLiveTask("codex-task:"+id, title)
	task.progress.ID = id
	if parent != tasks.root {
		task.progress.ParentID = parent
	}
	tasks.tasks[id] = task
	tasks.order = append(tasks.order, task)
	return task
}

func (tasks *codexTasks) running() bool {
	for _, task := range tasks.order {
		if task.running() {
			return true
		}
	}
	return false
}

// A child that reports its own events needs no reads while it keeps doing so.
const codexLiveQuiet = 10 * time.Second

// Child states arrive in the same two spellings as the items carrying them.
func codexChildStatus(status string) string {
	switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(status)) {
	case "pendinginit", "running", "inprogress", "active":
		return chatmodel.ToolRunning
	case "completed":
		return chatmodel.ToolCompleted
	case "errored", "failed", "systemerror":
		return chatmodel.ToolFailed
	case "interrupted", "shutdown":
		return "stopped"
	case "notfound", "notloaded":
		return "unknown"
	}
	return ""
}

func codexTaskInput(tool string) bool {
	return tool == "resumeagent" || tool == "followuptask" || tool == "sendinput"
}

// Track calls seen on either transport path so saved snapshots cannot replay
// work already observed live. Missing identities cannot prove fresh work.
func (tasks *codexTasks) observeCall(thread, id, receiver string) bool {
	if id == "" {
		return false
	}
	if tasks.calls == nil {
		tasks.calls = map[codexTaskCall]bool{}
	}
	key := codexTaskCall{thread: thread, id: id, receiver: receiver}
	fresh := !tasks.calls[key]
	tasks.calls[key] = true
	return fresh
}

// The children a collaboration call addresses, in a stable order, and what
// the call says about each.
func codexReceivers(item map[string]any) ([]string, map[string]any) {
	states, _ := itemField(item, "agentsStates", "agents_states").(map[string]any)
	raw, _ := itemField(item, "receiverThreadIds", "receiver_thread_ids").([]any)
	ids := make([]string, 0, max(len(raw), len(states)))
	for _, value := range raw {
		if id := stringValue(value); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		for id := range states {
			if id != "" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
	}
	return ids, states
}

func (tasks *codexTasks) item(item map[string]any, parent string, emit func(AgentEvent) error) error {
	switch codexItemKind(item) {
	case "collabagenttoolcall":
		tool := strings.ToLower(stringValue(item["tool"]))
		ids, states := codexReceivers(item)
		for _, id := range ids {
			task := tasks.tasks[id]
			if task == nil && tool != "spawnagent" && states[id] == nil {
				continue
			}
			if task == nil {
				task = tasks.ensure(id, parent, stringValue(item["prompt"]))
			}
			if task == nil {
				continue
			}
			tasks.observeCall(parent, stringValue(item["id"]), id)
			if prompt := strings.TrimSpace(stringValue(item["prompt"])); prompt != "" && tool == "spawnagent" {
				task.part.Name = chatmodel.Clip(prompt, 240)
				task.description = chatmodel.Clip(prompt, 2000)
			}
			if codexTaskInput(tool) {
				task.status = chatmodel.ToolRunning
			}
			state, _ := states[id].(map[string]any)
			if status := codexChildStatus(stringValue(state["status"])); status != "" {
				if status != chatmodel.ToolRunning || task.running() || task.status == "unknown" {
					task.status = status
				}
			} else if (tool == "closeagent" || tool == "interruptagent") && item["status"] == "completed" {
				task.status = "stopped"
			}
			// The spawn tool's completion only acknowledges creation.
			if message := strings.TrimSpace(stringValue(state["message"])); message != "" {
				task.progress.Summary = chatmodel.Clip(message, 4000)
			}
			if err := emitTask(task, emit); err != nil {
				return err
			}
		}
	case "subagentactivity":
		task := tasks.ensure(stringValue(item["agentThreadId"]), parent, stringValue(item["agentPath"]))
		if task == nil {
			return nil
		}
		switch item["kind"] {
		case "started":
			if task.status == "unknown" {
				task.status = chatmodel.ToolRunning
			}
		case "interacted":
			task.status = chatmodel.ToolRunning
		case "completed":
			task.status = chatmodel.ToolCompleted
		case "interrupted":
			task.status = "stopped"
		}
		if err := emitTask(task, emit); err != nil {
			return err
		}
	}
	return nil
}

// Saved collaboration items discover descendants and new calls that reactivate
// them. Repeated calls and old spawn/wait snapshots retain newer live states.
func (tasks *codexTasks) discover(item map[string]any, parent string, emit func(AgentEvent) error) error {
	switch codexItemKind(item) {
	case "collabagenttoolcall":
		input := codexTaskInput(strings.ToLower(stringValue(item["tool"])))
		ids, states := codexReceivers(item)
		fresh := []any{}
		for _, id := range ids {
			freshCall := tasks.observeCall(parent, stringValue(item["id"]), id)
			reactivate := freshCall && input
			state, _ := states[id].(map[string]any)
			status := codexChildStatus(stringValue(state["status"]))
			// Reads may contain older completed followups after the newest
			// running call. Only fresh active work may change a known child;
			// its own notifications or polling settle that work.
			if task := tasks.tasks[id]; task == nil || reactivate && !task.running() && (status == "" || status == chatmodel.ToolRunning) {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return nil
		}
		copy := make(map[string]any, len(item))
		for key, value := range item {
			copy[key] = value
		}
		copy["receiverThreadIds"] = fresh
		return tasks.item(copy, parent, emit)
	case "subagentactivity":
		if tasks.tasks[stringValue(item["agentThreadId"])] == nil {
			return tasks.item(item, parent, emit)
		}
	}
	return nil
}

// Observe only descendants of this chat. Child text updates task summaries,
// never the main reply. Thread notifications can precede the spawn result.
func (tasks *codexTasks) event(method string, params map[string]any, emit func(AgentEvent) error) (bool, error) {
	id := stringValue(params["threadId"])
	if method == "thread/started" {
		thread, _ := params["thread"].(map[string]any)
		parent := stringValue(thread["parentThreadId"])
		if parent == "" {
			source, _ := thread["source"].(map[string]any)
			agent, _ := source["subAgent"].(map[string]any)
			parent = nestedString(agent, "thread_spawn", "parent_thread_id")
		}
		if parent != tasks.root && tasks.tasks[parent] == nil {
			return false, nil
		}
		task := tasks.ensure(stringValue(thread["id"]), parent, stringValue(thread["preview"]))
		if task == nil {
			return false, nil
		}
		task.progress.AgentType = chatmodel.Clip(stringValue(thread["agentRole"]), 200)
		if task.part.Name == "Subagent" && stringValue(thread["agentNickname"]) != "" {
			task.part.Name = chatmodel.Clip(stringValue(thread["agentNickname"]), 240)
		}
		return true, emitTask(task, emit)
	}
	task := tasks.tasks[id]
	if task == nil {
		return false, nil
	}
	tasks.heard(task)
	switch method {
	case "turn/started":
		tasks.turns[id] = nestedString(params, "turn", "id")
		task.status = chatmodel.ToolRunning
	case "turn/completed":
		if current, ended := tasks.turns[id], nestedString(params, "turn", "id"); current != "" && ended != "" && current != ended {
			return true, nil
		}
		tasks.turns[id] = nestedString(params, "turn", "id")
		if status := codexChildStatus(nestedString(params, "turn", "status")); status != "" {
			task.status = status
		}
		if text := nestedString(params, "turn", "error", "message"); text != "" {
			task.progress.Summary = chatmodel.Clip(text, 4000)
		}
	case "thread/status/changed":
		status := nestedString(params, "status", "type")
		if status == "systemError" || status == "notLoaded" || status == "active" && (task.running() || task.status == "unknown") {
			task.status = codexChildStatus(status)
		}
	case "thread/closed":
		task.status = "stopped"
	case "item/agentMessage/delta":
		if task.running() {
			task.progress.Summary = chatmodel.Clip(task.progress.Summary+stringValue(params["delta"]), 4000)
		}
	case "item/started", "item/completed":
		item, _ := params["item"].(map[string]any)
		if err := tasks.item(item, id, emit); err != nil {
			return true, err
		}
		step, ok := codexTaskStep(item)
		if ok && step.Kind == chatmodel.TaskStepMessage {
			task.progress.Summary = chatmodel.Clip(stringValue(item["text"]), 4000)
		} else if ok {
			task.tool(step.ID, step.Name)
		}
		if ok {
			task.step(step)
		}
	}
	return true, emitTask(task, emit)
}

// The child's own turn and item events are reaching this client: its earlier
// unanswered reads say nothing about it, and reading it would only repeat them.
func (tasks *codexTasks) heard(task *liveTask) {
	id := task.progress.ID
	// Freshness follows native activity even when the displayed row is unchanged.
	task.revision++
	tasks.failures[id] = 0
	tasks.next[id] = time.Now().Add(codexLiveQuiet)
}

// A request from a known descendant belongs to this chat. It proves the child
// is working, whatever its last read said, and names the turn it is working
// in: a followup's turn/started may not have been forwarded or polled yet.
func (tasks *codexTasks) requester(method string, params map[string]any, emit func(AgentEvent) error) (thread, turn string, err error) {
	thread = stringValue(params["threadId"])
	if method == "execCommandApproval" || method == "applyPatchApproval" {
		thread = stringValue(params["conversationId"])
	}
	task := tasks.tasks[thread]
	if task == nil {
		return "", "", nil
	}
	if turn = stringValue(params["turnId"]); turn != "" {
		tasks.turns[thread] = turn
	} else {
		turn = tasks.turns[thread]
	}
	tasks.heard(task)
	if !task.running() {
		if task.status == "unknown" {
			task.progress.Summary = ""
		}
		task.status = chatmodel.ToolRunning
		err = emitTask(task, emit)
	}
	return thread, turn, err
}

// Poll bounded recent pages on the same transport when child notifications are
// not forwarded. Reading never resumes a child or takes its writer lease.
func (tasks *codexTasks) poll(p *agentProcess, now time.Time, emit func(AgentEvent) error) error {
	busy := map[string]bool{}
	for id, read := range tasks.reads {
		if now.Sub(read.sent) < 10*time.Second {
			busy[read.task.progress.ID] = true
			continue
		}
		delete(tasks.reads, id)
		if read.revision != read.task.revision {
			continue
		}
		if err := tasks.readFailure(read.task, "Timed out reading subagent progress.", emit); err != nil {
			return err
		}
	}
	count := 0
	for _, task := range tasks.order {
		if len(tasks.reads) >= 8 {
			break
		}
		id := task.progress.ID
		if !task.running() || busy[id] || now.Before(tasks.next[id]) || count >= 8 {
			continue
		}
		if err := tasks.request(p, task, "thread/turns/list", map[string]any{"threadId": id, "limit": 1, "itemsView": "summary", "sortDirection": "desc"}, now); err != nil {
			return err
		}
		tasks.next[id] = now.Add(2 * time.Second)
		count++
	}
	return nil
}

func (tasks *codexTasks) request(p *agentProcess, task *liveTask, method string, params map[string]any, now time.Time) error {
	p.nextID++
	id := fmt.Sprintf("spool-task-%d", p.nextID)
	if err := p.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	tasks.reads[id] = codexTaskRead{task: task, method: method, sent: now, revision: task.revision}
	return nil
}

// Only consecutive reads that told nothing count: any answer about the child,
// or any event from it, starts the count again.
func (tasks *codexTasks) readFailure(task *liveTask, message string, emit func(AgentEvent) error) error {
	if !task.running() {
		return nil
	}
	tasks.failures[task.progress.ID]++
	if tasks.failures[task.progress.ID] >= 3 {
		task.status, task.progress.Summary = "unknown", chatmodel.Clip(message, 1000)
		return emitTask(task, emit)
	}
	return nil
}

func (tasks *codexTasks) response(p *agentProcess, value map[string]any, emit func(AgentEvent) error) (bool, error) {
	read, ok := tasks.reads[stringValue(value["id"])]
	if !ok || value["method"] != nil {
		return false, nil
	}
	delete(tasks.reads, stringValue(value["id"]))
	task := read.task
	// A delayed read must not overwrite a newer live notification.
	if task.revision != read.revision {
		return true, nil
	}
	if value["error"] != nil {
		return true, tasks.readFailure(task, "Subagent progress is unavailable: "+nestedString(value, "error", "message"), emit)
	}
	body, _ := value["result"].(map[string]any)
	data, _ := body["data"].([]any)
	if read.method == "thread/turns/list" {
		if len(data) == 0 {
			// The thread answered: a child still being set up has no turn yet.
			tasks.failures[task.progress.ID] = 0
			return true, nil
		}
		turn, _ := data[0].(map[string]any)
		id := stringValue(turn["id"])
		if id == "" {
			return true, tasks.readFailure(task, "Subagent returned no turn identity.", emit)
		}
		tasks.failures[task.progress.ID] = 0
		tasks.turns[task.progress.ID] = id
		if status := codexChildStatus(stringValue(turn["status"])); status != "" {
			task.status = status
		}
		if text := nestedString(turn, "error", "message"); text != "" {
			task.progress.Summary = chatmodel.Clip(text, 4000)
		}
		if err := emitTask(task, emit); err != nil {
			return true, err
		}
		if err := tasks.request(p, task, "thread/items/list", map[string]any{"threadId": task.progress.ID, "turnId": id, "limit": 16, "sortDirection": "desc", "deferOutput": map[string]any{}}, time.Now()); err != nil {
			return true, err
		}
		return true, nil
	} else {
		// Newest first: the latest stable message is the progress summary. The
		// page is bounded, and so are the steps kept from it: never copy the
		// child's full transcript into the chat.
		items := make([]map[string]any, 0, len(data))
		messageSeen := false
		for _, raw := range data {
			item, _ := raw.(map[string]any)
			if nested, ok := item["item"].(map[string]any); ok {
				item = nested
			}
			items = append(items, item)
			if err := tasks.discover(item, task.progress.ID, emit); err != nil {
				return true, err
			}
			if !messageSeen && isCodexAgentMessage(item["type"]) {
				if text := stringValue(item["text"]); text != "" {
					task.progress.Summary = chatmodel.Clip(text, 4000)
					messageSeen = true
				}
			}
		}
		// Steps are told in the order they happened, which leaves the most
		// recent tool as the last one. A page is read again and again: only an
		// item that names itself can be recognized as already told.
		for _, item := range slices.Backward(items) {
			step, ok := codexTaskStep(item)
			if !ok {
				continue
			}
			if step.Kind != chatmodel.TaskStepMessage {
				task.tool(step.ID, step.Name)
			}
			if step.ID != "" {
				task.step(step)
			}
		}
	}
	return true, emitTask(task, emit)
}

// A child's item as one of its steps: what it said, or the tool it called
// with Codex's own reading of that call.
func codexTaskStep(item map[string]any) (chatmodel.TaskStep, bool) {
	id := stringValue(item["id"])
	if isCodexAgentMessage(item["type"]) {
		text := strings.TrimSpace(stringValue(item["text"]))
		return chatmodel.TaskStep{ID: id, Kind: chatmodel.TaskStepMessage, Text: text}, text != ""
	}
	parts, ok := codexToolParts(item, "")
	if !ok && codexItemKind(item) == "imageview" {
		parts = codexTranscriptParts(item, "")
	}
	if len(parts) == 0 || parts[0].Name == "" {
		return chatmodel.TaskStep{}, false
	}
	step := chatmodel.TaskStep{ID: id, Name: parts[0].Name, Input: chatmodel.Clip(parts[0].Text, taskStepBytes)}
	// Bound structured fields before encoding so a long body cannot hide the
	// target or leave the step's display input as an incomplete JSON document.
	var input map[string]any
	if json.Unmarshal([]byte(parts[0].Text), &input) == nil && input != nil {
		step.Input = taskStepInput(input)
	}
	if native := parts[0].Native; native != nil {
		step.Kind, step.Path, step.Query, step.Status = native.Kind, native.Path, native.Query, native.Status
	}
	return step, true
}
