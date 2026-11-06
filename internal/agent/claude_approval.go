package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	chatmodel "spool/internal/model"
)

var approvalANSI = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)

func claudeApproval(body map[string]any) (approvalProtocol, bool) {
	switch body["subtype"] {
	case "can_use_tool":
		input, _ := body["input"].(map[string]any)
		if body["tool_name"] == "AskUserQuestion" {
			r := userInputRequest(input, true)
			return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
				if !response.Allow {
					return claudeDenied(response, body)
				}
				updated := map[string]any{}
				for key, value := range input {
					updated[key] = value
				}
				answers := map[string]any{}
				for key, value := range response.Content {
					switch value := value.(type) {
					case []any:
						values := make([]string, len(value))
						for i, entry := range value {
							values[i] = fmt.Sprint(entry)
						}
						answers[key] = strings.Join(values, ", ")
					default:
						answers[key] = fmt.Sprint(value)
					}
				}
				updated["answers"] = answers
				return map[string]any{"behavior": "allow", "updatedInput": updated, "toolUseID": body["tool_use_id"]}
			}}, true
		}
		r := chatmodel.ApprovalRequest{Title: chatmodel.Clip(stringValue(body["tool_name"]), 240), Detail: approvalDetail(input),
			Reason: chatmodel.Clip(approvalANSI.ReplaceAllString(stringValue(body["decision_reason"]), ""), 8192), Feedback: true,
			Decisions: []chatmodel.ApprovalDecision{{ID: "deny", Label: "Deny", Kind: "deny"}, {ID: "cancel", Label: "Deny and stop", Kind: "cancel"}, {ID: "allow", Label: "Allow once", Kind: "approve"}}}
		if title := stringValue(body["title"]); title != "" {
			r.Title = chatmodel.Clip(title, 240)
		}
		if description := stringValue(body["description"]); description != "" {
			r.Reason = chatmodel.Clip(description+"\n"+r.Reason, 8192)
		}
		if path := stringValue(body["blocked_path"]); path != "" {
			r.Detail = chatmodel.AppendBounded(r.Detail, "\nblocked_path: "+path, 24*1024)
		}
		// Editing is explicit and optional; old clients still approve the exact
		// original input. Submitted input must remain a JSON object.
		r.Fields = []chatmodel.ApprovalField{{Name: "input", Label: "Tool input", Description: "Review or edit the tool input before approving.", Type: "json", Default: input}}
		if body["requires_user_interaction"] == true && body["tool_name"] != "EnterPlanMode" && body["tool_name"] != "ExitPlanMode" {
			r.Unavailable = "This tool requires an interaction in its native application."
		}
		suggestions := map[string]any{}
		if entries, ok := body["permission_suggestions"].([]any); ok {
			for i, entry := range entries {
				update, ok := entry.(map[string]any)
				if !ok || !validClaudePermissionUpdate(update) {
					continue
				}
				if body["suppress_always_allow_rule"] == true && update["destination"] != "session" && update["destination"] != "cliArg" {
					continue
				}
				id := fmt.Sprintf("suggestion-%d", i)
				suggestions[id] = update
				label := "Allow and apply permission suggestion"
				if update["destination"] == "session" || update["destination"] == "cliArg" {
					label = "Allow for this session"
				}
				r.Decisions = append(r.Decisions, chatmodel.ApprovalDecision{ID: id, Label: label, Description: approvalDetail(update), Kind: "approve"})
			}
		}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			if !response.Allow {
				return claudeDenied(response, body)
			}
			updated := any(input)
			if edited, ok := response.Content["input"].(map[string]any); ok {
				updated = edited
			}
			if updated == nil {
				updated = map[string]any{}
			}
			result := map[string]any{"behavior": "allow", "updatedInput": updated}
			if body["tool_use_id"] != nil {
				result["toolUseID"] = body["tool_use_id"]
			}
			if update, ok := suggestions[response.Decision]; ok {
				result["updatedPermissions"] = []any{update}
			}
			return result
		}}, true
	case "elicitation":
		params := claudeElicitationParams(body)
		r := codexElicitation(params)
		if title := stringValue(body["title"]); title != "" {
			r.Title = chatmodel.Clip(title, 240)
		}
		if description := stringValue(body["description"]); description != "" {
			r.Detail = chatmodel.AppendBounded(description, "\n"+r.Detail, 8192)
		}
		if order, ok := body["property_order"].([]any); ok {
			r.Fields = orderApprovalFields(r.Fields, order)
		}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			return elicitationResult(r, params, response)
		}}, true
	case "request_user_dialog":
		return claudeDialog(body)
	}
	return approvalProtocol{}, false
}

func claudeDenied(response chatmodel.ApprovalResponse, body map[string]any) map[string]any {
	message := response.Feedback
	if strings.TrimSpace(message) == "" {
		message = "The user declined this operation."
	}
	result := map[string]any{"behavior": "deny", "message": message}
	if response.Decision == "cancel" {
		result["interrupt"] = true
	}
	if body["tool_use_id"] != nil {
		result["toolUseID"] = body["tool_use_id"]
	}
	return result
}

func claudeElicitationParams(body map[string]any) map[string]any {
	mode := stringValue(body["mode"])
	if mode == "" {
		mode = "form"
	}
	return map[string]any{"mode": mode, "serverName": body["mcp_server_name"], "message": body["message"], "requestedSchema": body["requested_schema"], "url": body["url"]}
}

func orderApprovalFields(fields []chatmodel.ApprovalField, order []any) []chatmodel.ApprovalField {
	result := make([]chatmodel.ApprovalField, 0, len(fields))
	seen := map[string]bool{}
	for _, name := range order {
		for _, field := range fields {
			if name == field.Name && !seen[field.Name] {
				result = append(result, field)
				seen[field.Name] = true
			}
		}
	}
	for _, field := range fields {
		if !seen[field.Name] {
			result = append(result, field)
		}
	}
	return result
}

func validClaudePermissionUpdate(update map[string]any) bool {
	switch update["destination"] {
	case "userSettings", "projectSettings", "localSettings", "session", "cliArg":
	default:
		return false
	}
	switch update["type"] {
	case "addRules", "replaceRules", "removeRules":
		rules, ok := update["rules"].([]any)
		if !ok || len(rules) == 0 {
			return false
		}
		for _, value := range rules {
			rule, ok := value.(map[string]any)
			if !ok || stringValue(rule["toolName"]) == "" {
				return false
			}
		}
		switch update["behavior"] {
		case "allow", "deny", "ask":
			return true
		}
	case "setMode":
		return update["mode"] == "default" || (chatmodel.ValidatePermissionMode(chatmodel.BackendClaude, stringValue(update["mode"])) == nil && stringValue(update["mode"]) != "")
	case "addDirectories", "removeDirectories":
		values, ok := update["directories"].([]any)
		if !ok || len(values) == 0 {
			return false
		}
		for _, value := range values {
			if stringValue(value) == "" {
				return false
			}
		}
		return true
	}
	return false
}

// Useful for serializable dialog results as well as native input values.
func copyApprovalObject(value any) map[string]any {
	data, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if result == nil {
		result = map[string]any{}
	}
	return result
}
