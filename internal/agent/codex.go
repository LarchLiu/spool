package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

func (p *agentProcess) codexRPC(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.nextID++
	id := p.nextID
	if err := p.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		value, err := p.read(ctx)
		if err != nil {
			return nil, err
		}
		if value["id"] == float64(id) && value["method"] == nil {
			if rpcError, ok := value["error"].(map[string]any); ok {
				return nil, fmt.Errorf("Codex %s: %s", method, chatmodel.Clip(stringValue(rpcError["message"]), maxStderrBytes))
			}
			result, ok := value["result"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Codex %s returned no result", method)
			}
			return result, nil
		}
		if value["id"] != nil && value["method"] != nil {
			if method == "turn/start" {
				if len(p.queued) >= 1024 {
					return nil, fmt.Errorf("too many Codex requests before turn acknowledgement")
				}
				p.queued = append(p.queued, value)
			} else if err := p.denyCodexRequest(value); err != nil {
				return nil, err
			}
		} else if method == "turn/start" && value["method"] != nil {
			if len(p.queued) >= 1024 {
				return nil, fmt.Errorf("too many Codex events before turn acknowledgement")
			}
			p.queued = append(p.queued, value)
		}
	}
}

func (p *agentProcess) handleCodexApproval(ctx context.Context, value map[string]any, turnID string) error {
	params, _ := value["params"].(map[string]any)
	method := stringValue(value["method"])
	if stringValue(params["threadId"]) != p.sessionID || stringValue(params["turnId"]) != turnID {
		return p.denyCodexRequest(value)
	}
	request := chatmodel.ApprovalRequest{Title: "Command execution", Reason: chatmodel.Clip(stringValue(params["reason"]), 8192)}
	switch method {
	case "item/commandExecution/requestApproval":
		request.Detail = chatmodel.Clip(stringValue(params["command"]), 16*1024)
		for _, key := range []string{"cwd", "additionalPermissions", "networkApprovalContext"} {
			if params[key] != nil {
				request.Detail = chatmodel.AppendBounded(request.Detail, "\n"+key+": "+approvalDetail(params[key]), 24*1024)
			}
		}
		if options, ok := params["availableDecisions"].([]any); ok {
			canAccept := false
			for _, option := range options {
				if option == "accept" {
					canAccept = true
				}
			}
			if !canAccept {
				return p.denyCodexRequest(value)
			}
		}
	case "item/fileChange/requestApproval":
		request.Title, request.Detail = "Workspace file changes", stringValue(params["grantRoot"])
	case "item/permissions/requestApproval":
		request.Title, request.Detail = "Additional permissions", approvalDetail(params["permissions"])
	default:
		return p.denyCodexRequest(value)
	}
	allowed, err := p.awaitApproval(ctx, p.approve, request, func(event map[string]any) bool {
		params, _ := event["params"].(map[string]any)
		return event["method"] == "serverRequest/resolved" && fmt.Sprint(params["requestId"]) == fmt.Sprint(value["id"])
	})
	if err != nil {
		return err
	}
	result := map[string]any{"decision": "decline"}
	if allowed {
		result["decision"] = "accept"
	}
	if method == "item/permissions/requestApproval" {
		permissions := any(map[string]any{})
		if allowed {
			permissions = params["permissions"]
		}
		result = map[string]any{"permissions": permissions, "scope": "turn"}
	}
	return p.write(map[string]any{"id": value["id"], "result": result})
}

func (p *agentProcess) denyCodexRequest(value map[string]any) error {
	response := map[string]any{"id": value["id"]}
	switch stringValue(value["method"]) {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		response["result"] = map[string]any{"decision": "decline"}
	case "item/permissions/requestApproval":
		response["result"] = map[string]any{"permissions": map[string]any{}, "scope": "turn"}
	case "mcpServer/elicitation/request":
		response["result"] = map[string]any{"action": "decline"}
	default:
		response["error"] = map[string]any{"code": -32601, "message": "This web chat does not grant interactive permissions or provide client tools."}
	}
	return p.write(response)
}

func (p *agentProcess) runCodex(ctx context.Context, request AgentRequest, emit func(AgentEvent) error) (AgentResult, error, bool) {
	p.approve = request.Approve
	defer func() { p.approve = nil }()
	reviewer := "user"
	if request.PermissionMode == "auto" {
		reviewer = "auto_review"
	}
	result := AgentResult{SessionID: p.sessionID, Model: p.model, ReasoningEffort: p.effort}
	if err := p.initializeCodex(ctx); err != nil {
		return result, err, false
	}
	if p.sessionID == "" {
		params := map[string]any{
			"cwd": request.WorkingDirectory, "approvalPolicy": "on-request", "approvalsReviewer": reviewer,
			"sandbox": "workspace-write",
		}
		if request.Model != "" {
			params["model"] = request.Model
		}
		method := "thread/start"
		if request.NativeSessionID != "" {
			method = "thread/resume"
			params["threadId"] = request.NativeSessionID
			if request.NativeSessionPath != "" {
				params["path"] = request.NativeSessionPath
			}
			params["excludeTurns"] = true
		}
		response, err := p.codexRPC(ctx, method, params)
		if err != nil {
			return result, err, false
		}
		if applied := stringValue(response["approvalsReviewer"]); applied != "" && applied != reviewer {
			return result, fmt.Errorf("Codex did not apply the selected approval mode; no message was sent"), false
		}
		id := nestedString(response, "thread", "id")
		if !chatmodel.ValidID(id) || (request.NativeSessionID != "" && id != request.NativeSessionID) {
			return result, fmt.Errorf("Codex returned an invalid or different thread ID; no message was sent"), false
		}
		p.sessionID = id
		p.model = stringValue(response["model"])
		p.effort = stringValue(response["reasoningEffort"])
	} else if request.NativeSessionID != "" && request.NativeSessionID != p.sessionID {
		return result, fmt.Errorf("Codex session ID changed unexpectedly; no message was sent"), false
	}
	result.SessionID, result.Model = p.sessionID, p.model
	if err := emit(AgentEvent{Type: "session", SessionID: p.sessionID}); err != nil {
		return result, err, false
	}
	p.queued = nil
	input := []any{map[string]any{"type": "text", "text": agentTextInput(request), "text_elements": []any{}}}
	for _, file := range request.Attachments {
		if file.Image() {
			input = append(input, map[string]any{"type": "localImage", "path": file.Path})
		}
	}
	params := map[string]any{
		"threadId": p.sessionID, "cwd": request.WorkingDirectory,
		"approvalPolicy": "on-request", "approvalsReviewer": reviewer,
		"sandboxPolicy": map[string]any{"type": "workspaceWrite", "writableRoots": []string{request.WorkingDirectory}, "networkAccess": false},
		"input":         input,
	}
	if request.Model != "" {
		params["model"] = request.Model
	}
	if request.ReasoningEffort != "" {
		params["effort"] = request.ReasoningEffort
	}
	response, err := p.codexRPC(ctx, "turn/start", params)
	if err != nil {
		return result, err, false
	}
	turnID := nestedString(response, "turn", "id")
	if !chatmodel.ValidID(turnID) {
		return result, fmt.Errorf("Codex returned an invalid turn ID"), false
	}
	if request.Model != "" {
		p.model = request.Model
	}
	if request.Model != "" || request.ReasoningEffort != "" {
		p.effort = request.ReasoningEffort
	}
	result.Model, result.ReasoningEffort = p.model, p.effort
	if p.model != "" {
		if err := emit(AgentEvent{Type: "model", Model: p.model, ReasoningEffort: p.effort}); err != nil {
			return result, err, false
		}
	}
	items := make(map[string]string)
	startedItems := make(map[string]bool)
	streamedThinking := make(map[string]string)
	streamedToolOutput := make(map[string]string)
	for {
		var value map[string]any
		if len(p.queued) > 0 {
			value, p.queued = p.queued[0], p.queued[1:]
		} else {
			value, err = p.read(ctx)
			if err != nil {
				return result, err, false
			}
		}
		method := stringValue(value["method"])
		params, _ := value["params"].(map[string]any)
		if value["id"] != nil && method != "" {
			if err := p.handleCodexApproval(ctx, value, turnID); err != nil {
				return result, err, false
			}
			continue
		}
		if stringValue(params["threadId"]) != p.sessionID {
			continue
		}
		if method == "thread/settings/updated" {
			settings, _ := params["threadSettings"].(map[string]any)
			if model := stringValue(settings["model"]); model != "" {
				p.model = model
			}
			p.effort = stringValue(settings["effort"])
			result.Model, result.ReasoningEffort = p.model, p.effort
			if err := emit(AgentEvent{Type: "model", Model: p.model, ReasoningEffort: p.effort}); err != nil {
				return result, err, false
			}
			continue
		}
		eventTurn := stringValue(params["turnId"])
		if method == "turn/completed" || method == "turn/started" {
			eventTurn = nestedString(params, "turn", "id")
		}
		if eventTurn != turnID {
			continue
		}
		switch method {
		case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
			delta := stringValue(params["delta"])
			if delta == "" {
				continue
			}
			itemID := stringValue(params["itemId"])
			if itemID != "" {
				streamedThinking[itemID] = chatmodel.AppendBounded(streamedThinking[itemID], delta, chatmodel.MaxReplyBytes)
			}
			part := chatmodel.Part{Role: "assistant", Kind: "thinking", Model: result.Model, Text: delta}
			if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
				return result, err, false
			}
		case "item/commandExecution/outputDelta":
			delta := stringValue(params["delta"])
			itemID := stringValue(params["itemId"])
			if delta == "" || itemID == "" {
				continue
			}
			streamedToolOutput[itemID] += delta
			part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: delta}
			if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
				return result, err, false
			}
		case "item/fileChange/outputDelta":
			// Older Codex versions streamed apply_patch output through this
			// notification. Keep it in the same normalized result part as the
			// newer patchUpdated notification below.
			delta := stringValue(params["delta"])
			itemID := stringValue(params["itemId"])
			if delta == "" || itemID == "" {
				continue
			}
			streamedToolOutput[itemID] += delta
			part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: delta}
			if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
				return result, err, false
			}
		case "item/fileChange/patchUpdated":
			// patchUpdated carries the current complete set of file changes,
			// rather than a byte delta. Emit only the suffix when the update is
			// cumulative; this prevents the same diff being appended repeatedly.
			itemID := stringValue(params["itemId"])
			changes := params["changes"]
			if itemID == "" || changes == nil {
				continue
			}
			full := codexFileChangeDiff(map[string]any{"changes": changes})
			if full == "" {
				continue
			}
			previous := streamedToolOutput[itemID]
			delta := full
			if previous != "" {
				if strings.HasPrefix(full, previous) {
					delta = full[len(previous):]
				} else if full == previous {
					delta = ""
				}
			}
			streamedToolOutput[itemID] = full
			if delta == "" {
				continue
			}
			part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: delta}
			if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
				return result, err, false
			}
		case "item/agentMessage/delta":
			id, delta := stringValue(params["itemId"]), stringValue(params["delta"])
			if len(items) >= 4096 && items[id] == "" {
				return result, fmt.Errorf("too many Codex message items"), false
			}
			items[id] = chatmodel.AppendBounded(items[id], delta, chatmodel.MaxReplyBytes)
			result.Text = chatmodel.AppendBounded(result.Text, delta, chatmodel.MaxReplyBytes)
			if delta != "" {
				if err := emit(AgentEvent{Type: "delta", Text: delta}); err != nil {
					return result, err, false
				}
			}
		case "item/started", "item/completed":
			item, _ := params["item"].(map[string]any)
			if isCodexAgentMessage(item["type"]) && method == "item/completed" {
				id, full := stringValue(item["id"]), stringValue(item["text"])
				previous := items[id]
				if strings.HasPrefix(full, previous) && len(full) > len(previous) {
					delta := full[len(previous):]
					result.Text = chatmodel.AppendBounded(result.Text, delta, chatmodel.MaxReplyBytes)
					items[id] = chatmodel.Clip(full, chatmodel.MaxReplyBytes)
					if err := emit(AgentEvent{Type: "delta", Text: delta}); err != nil {
						return result, err, false
					}
				}
			} else if !isCodexAgentMessage(item["type"]) && item["type"] != "userMessage" {
				id := stringValue(item["id"])
				started := method == "item/started"
				if started && id != "" {
					startedItems[id] = true
				}
				for _, part := range codexTranscriptParts(item, result.Model) {
					if part.Kind == "tool_result" {
						if started {
							continue
						}
						full := part.Text
						if id != "" {
							if previous := streamedToolOutput[id]; previous != "" {
								if strings.HasPrefix(full, previous) {
									part.Text = full[len(previous):]
								} else if full == previous {
									part.Text = ""
								}
							}
							streamedToolOutput[id] = full
						}
						if part.Text == "" {
							continue
						}
						activity := "Tool result"
						result.Activities = append(result.Activities, activity)
						if err := emit(AgentEvent{Type: "activity", Text: activity, Part: &part}); err != nil {
							return result, err, false
						}
						continue
					}
					if part.Kind == "thinking" {
						if id != "" {
							full := part.Text
							previous := streamedThinking[id]
							if previous != "" {
								if strings.HasPrefix(full, previous) {
									part.Text = full[len(previous):]
								} else if full == previous {
									part.Text = ""
								}
							}
							streamedThinking[id] = full
						}
						if part.Text == "" {
							continue
						}
						if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
							return result, err, false
						}
						continue
					}
					if part.Kind != "tool_use" || (!started && id != "" && startedItems[id]) {
						continue
					}
					activity := codexActivity(item)
					if activity == "" {
						activity = "Using " + part.Name
					}
					result.Activities = append(result.Activities, activity)
					if err := emit(AgentEvent{Type: "activity", Text: activity, Part: &part}); err != nil {
						return result, err, false
					}
				}
			}
		case "turn/completed":
			switch nestedString(params, "turn", "status") {
			case "completed":
				if strings.TrimSpace(result.Text) == "" {
					return result, fmt.Errorf("Codex finished without an assistant message"), true
				}
				return result, nil, true
			case "interrupted":
				return result, fmt.Errorf("Codex turn interrupted: %w", context.Canceled), true
			default:
				message := nestedString(params, "turn", "error", "message")
				if message == "" {
					message = "turn failed without an error message"
				}
				return result, fmt.Errorf("Codex turn failed: %s", chatmodel.Clip(message, maxStderrBytes)), true
			}
		}
	}
}

func isCodexAgentMessage(value any) bool {
	kind := strings.ToLower(strings.ReplaceAll(stringValue(value), "_", ""))
	return kind == "agentmessage"
}
