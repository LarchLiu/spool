package workspace

import (
	"encoding/json"
	"slices"
	"strings"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func (s *Store) remoteAgentHistory(chat chatmodel.Session) ([]remoteAgentCall, error) {
	path := chat.NativeSessionPath
	if path == "" {
		directory := ""
		if project, err := s.Project(chat.ProjectID); err == nil {
			directory = project.Path
		}
		var err error
		path, err = agent.FindSessionTranscript(chat.Backend, chat.NativeSessionID, directory)
		if err != nil {
			return nil, err
		}
	}
	var calls []remoteAgentCall
	// Read the complete cached rollout, independent of the currently loaded
	// history page. The native UI history can also omit lazy tool bodies.
	err := s.historyCache.read(chat.Backend, path, func(file *nativeHistory) error {
		transcripts, err := s.historyCache.transcripts(file)
		if err != nil {
			return err
		}
		chat.Transcript = chatmodel.Transcript{}
		for _, transcript := range transcripts {
			chat.Transcript.Parts = append(chat.Transcript.Parts, transcript.Parts...)
		}
		calls = remoteCallsFromHistory(chat)
		return nil
	})
	return calls, err
}

// remoteHistoryResult accepts the native MCP result envelopes, including the
// timing prefix written by older Codex versions. It never searches prose for
// embedded JSON: the complete tool output must be one recognizable envelope.
func remoteHistoryResult(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "Wall time: ") {
		_, text, _ = strings.Cut(text, "\nOutput:\n")
	}
	data := json.RawMessage(text)
	for range 5 {
		if len(data) > remoteAgentMaxBody || !json.Valid(data) {
			return nil
		}
		var envelope struct {
			Result     json.RawMessage `json:"result"`
			Structured json.RawMessage `json:"structuredContent"`
			IsError    bool            `json:"isError"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(data, &envelope) != nil {
			return data // discovery and calls return arrays
		}
		if envelope.IsError {
			return nil
		}
		switch {
		case len(envelope.Structured) > 0:
			data = envelope.Structured
		case len(envelope.Result) > 0:
			data = envelope.Result
		case len(envelope.Content) == 1 && envelope.Content[0].Type == "text":
			data = json.RawMessage(envelope.Content[0].Text)
		default:
			return data
		}
	}
	return nil
}

// Native transcripts are reference material, not proof of a delegation by this
// server. These snapshots stay out of the database and grant no remote actions.
func remoteCallsFromHistory(chat chatmodel.Session) []remoteAgentCall {
	uses := map[string]remoteAgentOperation{}
	servers := map[string]string{}
	projects := map[string]string{}
	calls := map[string]remoteAgentCall{}
	order := []string{}
	remember := func(call remoteAgentCall) {
		task := &call.Task
		if call.Server == "" || !chatmodel.ValidID(task.ID) || !chatmodel.ValidID(task.Conversation) || !chatmodel.ValidID(task.Project) || !task.Backend.Valid() {
			return
		}
		switch task.Status {
		case "running", "awaiting_approval", "completed", "failed", "stopped", "interrupted":
		default:
			return
		}
		key := call.Server + ":" + task.ID
		prior := calls[key]
		if call.Message == "" {
			call.Message = prior.Message
		}
		call.HistoryOnly, call.OriginChat = true, chat.ID
		call.RequestID = task.RequestID
		call.Message = chatmodel.Clip(call.Message, 8192)
		task.Approval, task.Progress = nil, ""
		if task.Status != "completed" {
			task.Text = ""
		}
		if len(task.Text) > 8192 {
			task.Text = chatmodel.Clip(task.Text, 8192)
			task.Truncated = true
		}
		if _, exists := calls[key]; !exists {
			order = append(order, key)
		}
		calls[key] = call
	}
	for _, part := range chat.Transcript.Parts {
		if part.ID == "" {
			continue
		}
		if part.Role == "assistant" && part.Kind == "tool_use" {
			name := strings.TrimPrefix(part.Name, "mcp__spool_federation__")
			name = strings.TrimPrefix(name, "mcp.spool_federation.")
			name = strings.TrimPrefix(name, "spool_federation.")
			op, ok := strings.CutPrefix(name, "spool_")
			if !ok {
				continue
			}
			var input remoteAgentOperation
			if json.Unmarshal([]byte(part.Text), &input) == nil {
				input.Operation = op
				uses[part.ID] = input
			}
			continue
		}
		input, ok := uses[part.ID]
		if !ok || part.Role != "tool" || part.Kind != "tool_result" {
			continue
		}
		result := remoteHistoryResult(part.Text)
		switch input.Operation {
		case "servers":
			var items []struct{ ID, Name string }
			if json.Unmarshal(result, &items) == nil {
				for _, item := range items {
					servers[item.ID] = item.Name
				}
			}
		case "projects":
			var items []struct{ ID, Name string }
			if json.Unmarshal(result, &items) == nil {
				for _, item := range items {
					projects[input.Server+":"+item.ID] = item.Name
				}
			}
		case "delegate", "task", "cancel":
			var task remoteAgentTask
			if json.Unmarshal(result, &task) != nil || input.TaskID != "" && input.TaskID != task.ID || input.Project != "" && input.Project != task.Project {
				continue
			}
			remember(remoteAgentCall{Server: input.Server, Message: input.Message, Task: task})
		case "calls":
			var items []remoteAgentCall
			if json.Unmarshal(result, &items) == nil {
				for _, item := range items {
					remember(item)
				}
			}
		}
	}
	slices.Reverse(order)
	items := make([]remoteAgentCall, 0, min(20, len(order)))
	for _, key := range order[:min(20, len(order))] {
		call := calls[key]
		call.ServerName = servers[call.Server]
		if call.Task.ProjectName == "" {
			call.Task.ProjectName = projects[call.Server+":"+call.Task.Project]
		}
		items = append(items, call)
	}
	return items
}
