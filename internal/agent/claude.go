package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

func (p *agentProcess) claudeControl(ctx context.Context, request map[string]any) (map[string]any, error) {
	// Only configuration acknowledgements have a deadline, never agent work.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.nextID++
	id := fmt.Sprintf("spool-control-%d", p.nextID)
	if err := p.write(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return nil, err
	}
	for {
		value, err := p.read(ctx)
		if err != nil {
			return nil, err
		}
		if value["type"] == "control_request" {
			if err = p.denyClaudeRequest(value); err != nil {
				return nil, err
			}
		}
		if value["type"] != "control_response" || nestedString(value, "response", "request_id") != id {
			continue
		}
		if nestedString(value, "response", "subtype") != "success" {
			return nil, fmt.Errorf("Claude %s failed: %s", request["subtype"], chatmodel.Clip(nestedString(value, "response", "error"), maxStderrBytes))
		}
		response, _ := value["response"].(map[string]any)
		body, _ := response["response"].(map[string]any)
		return body, nil
	}
}

func (p *agentProcess) denyClaudeRequest(value map[string]any) error {
	return p.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": value["request_id"],
		"response": map[string]any{"behavior": "deny", "message": "This web chat does not grant tool permissions."},
	}})
}

func (p *agentProcess) handleClaudeApproval(ctx context.Context, value map[string]any) error {
	if nestedString(value, "request", "subtype") != "can_use_tool" {
		return p.denyClaudeRequest(value)
	}
	body, _ := value["request"].(map[string]any)
	request := chatmodel.ApprovalRequest{Title: chatmodel.Clip(stringValue(body["tool_name"]), 240), Detail: approvalDetail(body["input"]), Reason: chatmodel.Clip(stringValue(body["decision_reason"]), 8192)}
	allowed, err := p.awaitApproval(ctx, p.approve, request, func(event map[string]any) bool {
		return event["type"] == "control_cancel_request" && event["request_id"] == value["request_id"]
	})
	if err != nil {
		return err
	}
	response := map[string]any{"behavior": "deny", "message": "The user declined this operation."}
	if allowed {
		// Approve only this exact input. Never create persistent allow rules.
		response = map[string]any{"behavior": "allow", "updatedInput": body["input"]}
		if response["updatedInput"] == nil {
			response["updatedInput"] = map[string]any{}
		}
	}
	return p.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": value["request_id"], "response": response}})
}

func (p *agentProcess) runClaude(ctx context.Context, request AgentRequest, emit func(AgentEvent) error) (AgentResult, error, bool) {
	p.approve = request.Approve
	defer func() { p.approve = nil }()
	result := AgentResult{SessionID: p.sessionID, Model: p.model}
	if !p.initialized {
		response, err := p.claudeControl(ctx, map[string]any{"subtype": "initialize", "hooks": nil})
		if err != nil {
			return result, err, false
		}
		commands, _ := response["commands"].([]any)
		for _, entry := range commands {
			command, _ := entry.(map[string]any)
			if command["name"] == "goal" && command["builtin"] == true {
				p.claudeGoalSupported = true
			}
		}
		p.initialized = true
	}
	if command, _ := chatmodel.ParseGoalCommand(request.Prompt); command != nil && !p.claudeGoalSupported {
		return result, fmt.Errorf("Installed Claude Code does not advertise native /goal support; update the CLI or check its policy (no prompt fallback)"), true
	}
	if p.sessionID == "" {
		p.sessionID = request.NativeSessionID
	} else if request.NativeSessionID != "" && request.NativeSessionID != p.sessionID {
		return result, fmt.Errorf("Claude session ID changed unexpectedly; no message was sent"), false
	}
	modelChanged := request.Model != p.selectedModel
	if modelChanged {
		var model any
		if request.Model != "" {
			model = request.Model
		}
		if _, err := p.claudeControl(ctx, map[string]any{"subtype": "set_model", "model": model}); err != nil {
			return result, fmt.Errorf("Claude model selection failed: %w", err), false
		}
		p.selectedModel, p.model = request.Model, request.Model
	}
	mode := request.PermissionMode
	if mode == "" {
		mode = chatmodel.DefaultPermissionMode(chatmodel.BackendClaude)
	}
	if mode != p.permissionMode || modelChanged {
		response, err := p.claudeControl(ctx, map[string]any{"subtype": "set_permission_mode", "mode": mode})
		if err != nil {
			return result, fmt.Errorf("Claude permission mode selection failed: %w", err), false
		}
		if applied := stringValue(response["mode"]); applied != "" && applied != mode && !(mode == "manual" && applied == "default") {
			return result, fmt.Errorf("Claude did not apply the selected permission mode; no message was sent"), false
		}
		p.permissionMode = mode
	}
	if request.ReasoningEffort != p.effort || (modelChanged && request.ReasoningEffort != "") {
		var effort any
		if request.ReasoningEffort != "" {
			effort = request.ReasoningEffort
		}
		// The flag layer is session-local; it never writes Claude settings files.
		if _, err := p.claudeControl(ctx, map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effort}}); err != nil {
			return result, fmt.Errorf("Claude reasoning level selection failed; update Claude Code if unsupported: %w", err), false
		}
		p.effort = request.ReasoningEffort
	}
	if request.ReasoningEffort != "" {
		settings, err := p.claudeControl(ctx, map[string]any{"subtype": "get_settings"})
		if err != nil {
			return result, fmt.Errorf("Claude reasoning level confirmation failed: %w", err), false
		}
		if applied := nestedString(settings, "applied", "effort"); applied != request.ReasoningEffort {
			return result, fmt.Errorf("Claude applied reasoning level %q instead of %q; check CLI settings or policy", applied, request.ReasoningEffort), false
		}
	}
	result.Model = p.model
	result.ReasoningEffort = p.effort
	var content any = agentTextInput(request)
	if len(request.Attachments) > 0 {
		parts := []any{map[string]any{"type": "text", "text": agentTextInput(request)}}
		for _, file := range request.Attachments {
			if !file.Image() {
				continue
			}
			data, err := os.ReadFile(file.Path)
			if err != nil {
				return result, err, false
			}
			parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": file.MediaType, "data": base64.StdEncoding.EncodeToString(data)}})
		}
		content = parts
	}
	if err := p.write(map[string]any{"type": "user", "session_id": p.sessionID,
		"message": map[string]any{"role": "user", "content": content}, "parent_tool_use_id": nil}); err != nil {
		return result, err, false
	}
	result.SessionID = p.sessionID
	if result.Model != "" {
		if err := emit(AgentEvent{Type: "model", Model: result.Model, ReasoningEffort: p.effort}); err != nil {
			return result, err, false
		}
	}
	streamed := false
	seenActivity := make(map[string]string)
	var streamedThinking string
	for {
		var value map[string]any
		var err error
		if len(p.queued) > 0 {
			value, p.queued = p.queued[0], p.queued[1:]
		} else {
			value, err = p.read(ctx)
		}
		if err != nil {
			return result, err, false
		}
		// Nested agent output is not the top-level chat response.
		if value["parent_tool_use_id"] != nil && value["parent_tool_use_id"] != "" && value["type"] != "control_request" {
			continue
		}
		switch stringValue(value["type"]) {
		case "control_request":
			if err := p.handleClaudeApproval(ctx, value); err != nil {
				return result, err, false
			}
		case "system":
			if stringValue(value["subtype"]) != "init" {
				continue
			}
			if id := stringValue(value["session_id"]); chatmodel.ValidID(id) {
				if p.sessionID != "" && id != p.sessionID {
					return result, fmt.Errorf("Claude returned a different session ID"), false
				}
				p.sessionID, result.SessionID = id, id
				if err := emit(AgentEvent{Type: "session", SessionID: id}); err != nil {
					return result, err, false
				}
			}
			if model := stringValue(value["model"]); model != "" {
				p.model, result.Model = model, model
				if err := emit(AgentEvent{Type: "model", Model: model, ReasoningEffort: p.effort}); err != nil {
					return result, err, false
				}
			}
		case "stream_event":
			if nestedString(value, "event", "type") == "content_block_delta" {
				deltaType := nestedString(value, "event", "delta", "type")
				switch deltaType {
				case "text_delta":
					text := nestedString(value, "event", "delta", "text")
					result.Text += text
					if text != "" {
						streamed = true
						if err := emit(AgentEvent{Type: "delta", Text: text}); err != nil {
							return result, err, false
						}
					}
				case "thinking_delta":
					text := nestedString(value, "event", "delta", "thinking")
					streamedThinking += text
					part := chatmodel.Part{Role: "assistant", Kind: "thinking", Model: result.Model, Text: text}
					if part.Text != "" {
						if err := emit(AgentEvent{Type: "part", Part: &part}); err != nil {
							return result, err, false
						}
					}
				}
			}
		case "assistant":
			message, _ := value["message"].(map[string]any)
			if model := stringValue(message["model"]); model != "" && model != "<synthetic>" && model != result.Model {
				p.model, result.Model = model, model
				if err := emit(AgentEvent{Type: "model", Model: model, ReasoningEffort: p.effort}); err != nil {
					return result, err, false
				}
			}
			content, _ := message["content"].([]any)
			for _, part := range content {
				item, _ := part.(map[string]any)
				switch stringValue(item["type"]) {
				case "thinking":
					// Depending on Claude CLI version, thinking can arrive either
					// as thinking_delta events or only in the completed assistant
					// message. Emit the missing suffix so both paths normalize to a
					// single Magpie thinking part.
					text := stringValue(item["thinking"])
					if text == "" {
						continue
					}
					delta := text
					if strings.HasPrefix(text, streamedThinking) {
						delta = text[len(streamedThinking):]
					}
					streamedThinking = text
					if delta == "" {
						continue
					}
					thinking := chatmodel.Part{Role: "assistant", Kind: "thinking", Model: result.Model, Text: delta}
					if err := emit(AgentEvent{Type: "part", Part: &thinking}); err != nil {
						return result, err, false
					}
				case "tool_use":
					id := stringValue(item["id"])
					if id == "" {
						id = stringValue(item["name"])
					}
					name := stringValue(item["name"])
					if id != "" && seenActivity[id] == "" {
						seenActivity[id] = name
						activity := "Using " + stringValue(item["name"])
						result.Activities = append(result.Activities, activity)
						part := claudeToolUsePart(item, result.Model)
						if err := emit(AgentEvent{Type: "activity", Text: activity, Part: &part}); err != nil {
							return result, err, false
						}
					}
				}
			}
		case "user":
			message, _ := value["message"].(map[string]any)
			content, _ := message["content"].([]any)
			for _, block := range content {
				item, _ := block.(map[string]any)
				if stringValue(item["type"]) != "tool_result" {
					continue
				}
				name := seenActivity[stringValue(item["tool_use_id"])]
				part := claudeToolResultPart(item)
				if diff := claudeToolResultDiff(value["toolUseResult"]); diff != "" {
					part.Text = strings.TrimSpace(strings.TrimSpace(part.Text) + "\n" + diff)
				}
				if part.Text == "" {
					continue
				}
				activity := "Tool result"
				if name != "" {
					activity += ": " + name
				}
				result.Activities = append(result.Activities, activity)
				if err := emit(AgentEvent{Type: "activity", Text: activity, Part: &part}); err != nil {
					return result, err, false
				}
			}
		case "result":
			if id := stringValue(value["session_id"]); chatmodel.ValidID(id) {
				if p.sessionID != "" && id != p.sessionID {
					return result, fmt.Errorf("Claude returned a different session ID"), false
				}
				p.sessionID, result.SessionID = id, id
				if err := emit(AgentEvent{Type: "session", SessionID: id}); err != nil {
					return result, err, false
				}
			}
			full := stringValue(value["result"])
			if full != "" && (!streamed || strings.HasPrefix(full, result.Text)) {
				text := full
				if streamed {
					text = full[len(result.Text):]
				}
				result.Text += text
				if text != "" {
					if err := emit(AgentEvent{Type: "delta", Text: text}); err != nil {
						return result, err, false
					}
				}
			}
			if stringValue(value["subtype"]) != "success" || value["is_error"] == true {
				message := stringValue(value["error"])
				if message == "" {
					if errors, ok := value["errors"].([]any); ok {
						for _, item := range errors {
							message += stringValue(item) + "\n"
						}
					}
				}
				if strings.TrimSpace(message) == "" {
					message = "turn failed without an error message"
				}
				return result, fmt.Errorf("Claude Code turn failed: %s", chatmodel.Clip(message, maxStderrBytes)), true
			}
			if strings.TrimSpace(result.Text) == "" {
				return result, fmt.Errorf("Claude Code finished without an assistant message"), true
			}
			// Keep stdin open: another user message starts the next turn in this CLI.
			return result, nil, true
		}
	}
}
