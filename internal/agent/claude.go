package agent

import (
	"context"
	"encoding/base64"
	"errors"
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
	if nestedString(value, "request", "subtype") == "request_user_dialog" {
		// Unknown/undeclared dialogs may belong to another attached client.
		// A cancelled response would falsely record a human dismissal.
		return nil
	}
	if nestedString(value, "request", "subtype") == "elicitation" {
		return p.write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": value["request_id"], "response": map[string]any{"action": "decline", "content": nil},
		}})
	}
	return p.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": value["request_id"],
		"response": map[string]any{"behavior": "deny", "message": "This web chat does not grant tool permissions."},
	}})
}

func (p *agentProcess) handleClaudeApproval(ctx context.Context, value map[string]any, onSettled ...func(map[string]any) error) error {
	body, _ := value["request"].(map[string]any)
	protocol, ok := claudeApproval(body)
	if !ok {
		return p.denyClaudeRequest(value)
	}
	request := protocol.request
	settle := func() error {
		for _, notify := range onSettled {
			if err := notify(body); err != nil {
				return err
			}
		}
		return nil
	}
	withdrawn := false
	decision, err := p.awaitApproval(ctx, p.approve, request, func(event map[string]any) bool {
		withdrawn = event["type"] == "control_cancel_request" && event["request_id"] == value["request_id"]
		return withdrawn
	})
	if errors.Is(err, errApprovalResolved) {
		if withdrawn {
			return settle()
		}
		return nil
	}
	if err != nil {
		return err
	}
	decision, err = request.NormalizeResponse(decision)
	if err != nil {
		return err
	}
	if err := p.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": value["request_id"], "response": protocol.result(decision)}}); err != nil {
		return err
	}
	return settle()
}

func (p *agentProcess) runClaude(ctx context.Context, request AgentRequest, emit func(AgentEvent) error) (AgentResult, error, bool) {
	emit = compactionEvents(emit)
	p.approve = request.Approve
	defer func() { p.approve = nil }()
	result := AgentResult{SessionID: p.sessionID, Model: p.model}
	if !p.initialized {
		response, err := p.claudeControl(ctx, claudeInitializeRequest())
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
	messageTexts := make(map[string]string)
	messageOrder := []string{}
	wireMessages, retiredWireMessages := map[string]string{}, map[string]bool{}
	messagePhases := make(map[string]string)
	currentMessage, lastTextMessage := "", ""
	messageStarted := false
	messageNumber := 0
	newMessage := func() string {
		messageNumber++
		return fmt.Sprintf("claude-message-%d", messageNumber)
	}
	emitText := func(id, text string) error {
		if text == "" {
			return nil
		}
		if _, exists := messageTexts[id]; !exists {
			messageOrder = append(messageOrder, id)
		}
		messageTexts[id] += text
		lastTextMessage = id
		result.Text += text
		part := chatmodel.Part{ID: id, Phase: messagePhases[id], Role: "assistant", Kind: "text", Model: result.Model, Text: text}
		return emit(AgentEvent{Type: "delta", Text: text, Part: &part})
	}
	setPhase := func(id, phase string) error {
		if id == "" || phase == "" || messagePhases[id] == phase {
			return nil
		}
		messagePhases[id] = phase
		part := chatmodel.Part{ID: id, Phase: phase, Role: "assistant", Kind: "text"}
		return emit(AgentEvent{Type: "message", Part: &part})
	}
	seenActivity := make(map[string]string)
	settleDialog := func(body map[string]any) error {
		if body["subtype"] != "request_user_dialog" || body["dialog_kind"] != "refusal_fallback_prompt" {
			return nil
		}
		payload, _ := body["payload"].(map[string]any)
		uuids, _ := payload["retractedMessageUuids"].([]any)
		ids := []string{}
		for _, value := range uuids {
			uuid := stringValue(value)
			if uuid == "" {
				continue
			}
			retiredWireMessages[uuid] = true
			id := wireMessages[uuid]
			if id == "" {
				for _, queued := range p.queued {
					if queued["type"] == "assistant" && queued["uuid"] == uuid {
						id = nestedString(queued, "message", "id")
						break
					}
				}
			}
			if id != "" {
				ids = append(ids, id)
				delete(messageTexts, id)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		result.Text, lastTextMessage = "", ""
		for _, id := range messageOrder {
			if text, exists := messageTexts[id]; exists {
				result.Text += text
				lastTextMessage = id
			}
		}
		return emit(AgentEvent{Type: "retract", RetractedIDs: ids})
	}
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
			if err := p.handleClaudeApproval(ctx, value, settleDialog); err != nil {
				return result, err, false
			}
		case "system":
			if value["subtype"] == "status" || value["subtype"] == "compact_boundary" {
				if err := emit(AgentEvent{Type: "compaction", Compacting: value["subtype"] == "status" && value["status"] == "compacting"}); err != nil {
					return result, err, false
				}
				continue
			}
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
			switch nestedString(value, "event", "type") {
			case "message_start":
				messageStarted = true
				currentMessage = nestedString(value, "event", "message", "id")
				if currentMessage == "" {
					currentMessage = newMessage()
				}
				streamedThinking = ""
			case "message_delta":
				delta, _ := value["event"].(map[string]any)["delta"].(map[string]any)
				if err := setPhase(currentMessage, claudeMessagePhase(delta)); err != nil {
					return result, err, false
				}
			case "message_stop":
				currentMessage, messageStarted = "", false
			case "content_block_delta":
				deltaType := nestedString(value, "event", "delta", "type")
				switch deltaType {
				case "text_delta":
					text := nestedString(value, "event", "delta", "text")
					if currentMessage == "" {
						currentMessage = newMessage()
					}
					if err := emitText(currentMessage, text); err != nil {
						return result, err, false
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
			if retiredWireMessages[stringValue(value["uuid"])] {
				continue
			}
			if claudeCompactionRecord(value) {
				continue
			}
			message, _ := value["message"].(map[string]any)
			id := stringValue(message["id"])
			if id == "" || (!messageStarted && currentMessage != "") {
				id = currentMessage
			}
			if id == "" {
				id = newMessage()
			}
			if uuid := stringValue(value["uuid"]); uuid != "" {
				wireMessages[uuid] = id
			}
			if err := setPhase(id, claudeMessagePhase(message)); err != nil {
				return result, err, false
			}
			if model := stringValue(message["model"]); model != "" && model != "<synthetic>" && model != result.Model {
				p.model, result.Model = model, model
				if err := emit(AgentEvent{Type: "model", Model: model, ReasoningEffort: p.effort}); err != nil {
					return result, err, false
				}
			}
			content, _ := message["content"].([]any)
			remaining := messageTexts[id]
			for _, part := range content {
				item, _ := part.(map[string]any)
				switch stringValue(item["type"]) {
				case "text":
					text := stringValue(item["text"])
					if strings.HasPrefix(remaining, text) {
						remaining = remaining[len(text):]
						continue
					}
					if strings.HasPrefix(text, remaining) {
						text = text[len(remaining):]
					} else if strings.HasSuffix(remaining, text) {
						text = ""
					}
					remaining = ""
					if err := emitText(id, text); err != nil {
						return result, err, false
					}
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
			// Legacy streams can omit message_start; their next assistant event
			// must still get its own message boundary.
			if !messageStarted {
				currentMessage = ""
				streamedThinking = ""
			}
		case "user":
			if claudeCompactionRecord(value) {
				continue
			}
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
			if err := emit(AgentEvent{Type: "compaction"}); err != nil {
				return result, err, false
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
			full := stringValue(value["result"])
			if full != "" {
				id, text := lastTextMessage, full
				if id != "" && strings.HasPrefix(full, messageTexts[id]) {
					text = full[len(messageTexts[id]):]
				} else if id != "" && messagePhases[id] == "final_answer" && strings.HasSuffix(messageTexts[id], full) {
					text = ""
				} else {
					id = newMessage()
				}
				if id == "" {
					id = newMessage()
				}
				if value["subtype"] == "success" && value["is_error"] != true {
					if err := setPhase(id, "final_answer"); err != nil {
						return result, err, false
					}
				}
				if err := emitText(id, text); err != nil {
					return result, err, false
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
