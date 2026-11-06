package agent

import (
	"fmt"
	"strings"

	chatmodel "spool/internal/model"
)

type approvalProtocol struct {
	request chatmodel.ApprovalRequest
	result  func(chatmodel.ApprovalResponse) map[string]any
}

func codexApproval(method string, params map[string]any) (approvalProtocol, bool) {
	r := chatmodel.ApprovalRequest{Title: "Command execution", Reason: chatmodel.Clip(stringValue(params["reason"]), 8192)}
	switch method {
	case "execCommandApproval", "applyPatchApproval":
		r.Feedback = true
		r.Detail = approvalDetail(params["command"])
		if method == "applyPatchApproval" {
			r.Title, r.Detail = "Workspace file changes", approvalDetail(params["fileChanges"])
		}
		if params["cwd"] != nil {
			r.Detail = chatmodel.AppendBounded(r.Detail, "\ncwd: "+stringValue(params["cwd"]), 24*1024)
		}
		r.Decisions = []chatmodel.ApprovalDecision{{ID: "deny", Label: "Deny", Kind: "deny"}, {ID: "cancel", Label: "Cancel turn", Kind: "cancel"}, {ID: "allow", Label: "Allow once", Kind: "approve"}, {ID: "session", Label: "Allow for this session", Kind: "approve"}}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			choice, _ := r.Decision(response)
			var decision any = "approved"
			switch choice.ID {
			case "deny":
				message := response.Feedback
				if strings.TrimSpace(message) == "" {
					message = "The user declined this operation."
				}
				decision = map[string]any{"denied": map[string]any{"rejection": message}}
			case "cancel":
				decision = "abort"
			case "session":
				decision = "approved_for_session"
			}
			return map[string]any{"decision": decision}
		}}, true
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		if method == "item/fileChange/requestApproval" {
			r.Title, r.Detail = "Workspace file changes", stringValue(params["grantRoot"])
		} else {
			r.Detail = chatmodel.Clip(stringValue(params["command"]), 16*1024)
			for _, key := range []string{"cwd", "additionalPermissions", "networkApprovalContext"} {
				if params[key] != nil {
					r.Detail = chatmodel.AppendBounded(r.Detail, "\n"+key+": "+approvalDetail(params[key]), 24*1024)
				}
			}
		}
		options, supplied := params["availableDecisions"].([]any)
		if !supplied {
			options = []any{"decline", "cancel", "accept", "acceptForSession"}
			if amendment, ok := params["proposedExecpolicyAmendment"].([]any); ok && len(amendment) > 0 {
				options = append(options, map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": amendment}})
			}
			if amendments, ok := params["proposedNetworkPolicyAmendments"].([]any); ok {
				for _, amendment := range amendments {
					options = append(options, map[string]any{"applyNetworkPolicyAmendment": map[string]any{"network_policy_amendment": amendment}})
				}
			}
		}
		native := map[string]any{}
		for i, option := range options {
			choice := chatmodel.ApprovalDecision{ID: fmt.Sprintf("policy-%d", i), Kind: "approve"}
			switch option {
			case "accept":
				choice.ID, choice.Label = "allow", "Allow once"
			case "acceptForSession":
				choice.ID, choice.Label = "session", "Allow for this session"
			case "decline":
				choice.ID, choice.Label, choice.Kind = "deny", "Deny", "deny"
			case "cancel":
				choice.ID, choice.Label, choice.Kind = "cancel", "Cancel turn", "cancel"
			default:
				object, ok := option.(map[string]any)
				if !ok {
					continue
				}
				if rule, ok := object["acceptWithExecpolicyAmendment"].(map[string]any); ok {
					choice.Label, choice.Description = "Allow and save command rule", approvalDetail(rule["execpolicy_amendment"])
				} else if rule, ok := object["applyNetworkPolicyAmendment"].(map[string]any); ok {
					amendment, _ := rule["network_policy_amendment"].(map[string]any)
					switch amendment["action"] {
					case "allow":
						choice.Label = "Allow and save network rule"
					case "deny":
						choice.Label, choice.Kind = "Deny and save network rule", "deny"
					default:
						continue
					}
					choice.Description = stringValue(amendment["host"])
				} else {
					continue
				}
			}
			if _, exists := native[choice.ID]; exists {
				continue
			}
			r.Decisions = append(r.Decisions, choice)
			native[choice.ID] = option
		}
		if len(r.Decisions) == 0 {
			return approvalProtocol{}, false
		}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			choice, _ := r.Decision(response)
			return map[string]any{"decision": native[choice.ID]}
		}}, true
	case "item/permissions/requestApproval":
		return codexPermissionApproval(params), true
	case "item/tool/requestUserInput":
		r = userInputRequest(params, false)
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			answers := map[string]any{}
			if response.Allow {
				for _, field := range r.Fields {
					if answer, ok := response.Content[field.Name]; ok {
						answers[field.Name] = map[string]any{"answers": []any{answer}}
					}
				}
			}
			return map[string]any{"answers": answers}
		}}, true
	case "mcpServer/elicitation/request":
		r = codexElicitation(params)
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			return elicitationResult(r, params, response)
		}}, true
	}
	return approvalProtocol{}, false
}

func elicitationResult(request chatmodel.ApprovalRequest, params map[string]any, response chatmodel.ApprovalResponse) map[string]any {
	choice, _ := request.Decision(response)
	result := map[string]any{"action": "decline", "content": nil}
	if choice.Kind == "cancel" {
		result["action"] = "cancel"
	}
	if response.Allow {
		result["action"] = "accept"
		if stringValue(params["mode"]) != "url" {
			content := response.Content
			if content == nil {
				content = map[string]any{}
			}
			result["content"] = content
		}
		if strings.HasPrefix(choice.ID, "persist-") {
			result["_meta"] = map[string]any{"persist": strings.TrimPrefix(choice.ID, "persist-")}
		}
	}
	return result
}

func userInputRequest(params map[string]any, claude bool) chatmodel.ApprovalRequest {
	r := chatmodel.ApprovalRequest{Title: "Your input is needed", Kind: "input", Decisions: []chatmodel.ApprovalDecision{
		{ID: "deny", Label: "Cancel request", Kind: "cancel"},
		{ID: "allow", Label: "Send answers", Kind: "submit"},
	}}
	if title := stringValue(params["title"]); title != "" {
		r.Title = chatmodel.Clip(title, 240)
	}
	questions, ok := params["questions"].([]any)
	if !ok || len(questions) == 0 || len(questions) > 32 {
		r.Unavailable = "This request contains invalid questions."
		return r
	}
	seen := map[string]bool{}
	for _, entry := range questions {
		q, ok := entry.(map[string]any)
		if !ok {
			r.Unavailable = "This request contains invalid questions."
			return r
		}
		name := stringValue(q["id"])
		if claude {
			name = stringValue(q["question"])
		}
		if name == "" || seen[name] {
			r.Unavailable = "This request contains invalid questions."
			return r
		}
		seen[name] = true
		f := chatmodel.ApprovalField{Name: name, Label: chatmodel.Clip(stringValue(q["question"]), 8192), Type: "string", Required: !claude,
			Description: chatmodel.Clip(stringValue(q["description"]), 4096), AllowOther: q["isOther"] == true || claude, Secret: q["isSecret"] == true}
		minimumLength := 1
		f.MinLength = &minimumLength
		if options, ok := q["options"].([]any); ok {
			if len(options) > 100 {
				r.Unavailable = "This request contains invalid questions."
				return r
			}
			for _, value := range options {
				option, _ := value.(map[string]any)
				label := stringValue(option["label"])
				if label == "" {
					r.Unavailable = "This request contains invalid questions."
					return r
				}
				description := stringValue(option["description"])
				if preview := stringValue(option["preview"]); preview != "" {
					description += "\n" + preview
				}
				f.Options = append(f.Options, chatmodel.ApprovalOption{Value: label, Label: chatmodel.Clip(label, 1024), Description: chatmodel.Clip(description, 8192)})
			}
		}
		if q["multiSelect"] == true {
			f.Type = "array"
		}
		if q["kind"] == "text" {
			f.Options, f.Multiline = nil, true
		}
		if q["kind"] == "number" {
			f.Type, f.Default = "number", q["defaultValue"]
			if min, ok := q["min"].(float64); ok {
				f.Minimum = &min
			}
			if max, ok := q["max"].(float64); ok {
				f.Maximum = &max
			}
			if step, ok := q["step"].(float64); ok {
				f.Step = &step
			}
		}
		r.Fields = append(r.Fields, f)
	}
	return r
}
