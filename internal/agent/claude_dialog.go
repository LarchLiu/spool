package agent

import (
	"fmt"
	"sort"

	chatmodel "spool/internal/model"
)

type dialogOption struct{ value, label string }
type dialogDefinition struct {
	title   string
	options []dialogOption
}

// Only declare dialogs whose wire result is understood. JSX handles, host
// installation state and telemetry-dependent native UI aren't portable dialogs.
var claudeDialogs = map[string]dialogDefinition{
	"refusal_fallback_prompt":      {"Choose how to continue", []dialogOption{{"retry_fallback", "Retry with fallback model"}, {"edit_prompt", "Edit prompt"}, {"cancelled", "Cancel request"}}},
	"auto_mode_server_fallback":    {"Automatic review billing notice", []dialogOption{{"continue", "Continue in automatic mode"}, {"interrupt", "Stop turn"}, {"cancelled", "Cancel request"}}},
	"fable_overage_consent_prompt": {"Approve additional usage", []dialogOption{{"consent", "Approve additional usage"}, {"switch_default", "Switch to default model"}, {"cancelled", "Cancel request"}}},
	"ultraplan_choice":             {"Choose where to continue the plan", []dialogOption{{"here", "Continue here"}, {"fresh", "Start a fresh conversation"}, {"cancel", "Cancel request"}}},
	"ultraplan_launch":             {"Start planning", []dialogOption{{"run", "Start planning"}, {"cancel", "Cancel request"}}},
	"auto_default_nudge":           {"Change default permission mode", []dialogOption{{"accept", "Use automatic mode by default"}, {"decline", "Keep current mode"}, {"cancelled", "Cancel request"}}},
	"fullscreen_upsell":            {"Enable full screen", []dialogOption{{"accept", "Enable full screen"}, {"dismiss", "Dismiss"}, {"cancelled", "Cancel request"}}},
	"left_arrow_confirm":           {"Confirm conversation change", []dialogOption{{"confirm", "Confirm conversation change"}, {"stay", "Stay here"}, {"cancelled", "Cancel request"}}},
	"lsp_recommendation":           {"Language server recommendation", []dialogOption{{"yes", "Install plugin"}, {"no", "Not now"}, {"never", "Never suggest this plugin"}, {"disable", "Disable plugin suggestions"}, {"cancelled", "Cancel request"}}},
	"plugin_hint":                  {"Plugin recommendation", []dialogOption{{"yes", "Install plugin"}, {"no", "Not now"}, {"disable", "Disable plugin suggestions"}, {"cancelled", "Cancel request"}}},
	"mcp_elicitation_waiting":      {"Waiting for MCP authorization", []dialogOption{{"dismiss", "Dismiss"}, {"retry", "Retry"}, {"cancel", "Cancel request"}}},
	"cold_resume_quota":            {"Resume this conversation", []dialogOption{{"resume", "Resume conversation"}, {"new_conversation", "Start a fresh conversation"}, {"dismiss", "Dismiss"}, {"cancelled", "Cancel request"}}},
	"resume_return":                {"Review conversation context", []dialogOption{{"compact", "Compact context"}, {"continue", "Continue conversation"}, {"dismiss", "Dismiss"}, {"never", "Do not show again"}, {"cancelled", "Cancel request"}}},
	"newer_model_offer":            {"Choose a model for this conversation", []dialogOption{{"resume_on_newer_model", "Resume with newer model"}, {"keep", "Keep current model"}, {"dismiss", "Dismiss"}, {"cancelled", "Cancel request"}}},
	"auto_mode_setup_review":       {"Review automatic mode settings", []dialogOption{{"accept", "Apply automatic mode settings"}, {"decline", "Keep current settings"}, {"cancelled", "Cancel request"}}},
	"cost_threshold":               {"Review usage cost", []dialogOption{{"acknowledged", "Acknowledge usage cost"}, {"cancelled", "Cancel request"}}},
	"device_mcp_consent":           {"Allow device MCP tools", []dialogOption{{"allow", "Allow device MCP tools"}, {"deny", "Deny"}, {"not_now", "Not now"}}},
	"unattended_serving_consent":   {"Allow unattended access", []dialogOption{{"accept", "Allow unattended access"}, {"decline", "Deny"}, {"not_now", "Not now"}}},
}

func claudeInitializeRequest() map[string]any {
	kinds := []string{"auto_mode_outside_reads", "sandbox_network_access", "auto_mode_flagged_allow", "mcp_elicitation", "mcp_url_elicitation"}
	for kind := range claudeDialogs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return map[string]any{"subtype": "initialize", "hooks": nil, "supportedDialogKinds": kinds}
}

func claudeDialog(body map[string]any) (approvalProtocol, bool) {
	kind := stringValue(body["dialog_kind"])
	payload, _ := body["payload"].(map[string]any)
	if kind == "mcp_elicitation" || kind == "mcp_url_elicitation" {
		params := copyApprovalObject(payload["params"])
		params["serverName"] = payload["serverName"]
		if params["mode"] == nil {
			params["mode"] = "form"
			if kind == "mcp_url_elicitation" {
				params["mode"] = "url"
			}
		}
		r := codexElicitation(params)
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			return map[string]any{"behavior": "completed", "result": elicitationResult(r, params, response)}
		}}, true
	}
	r := chatmodel.ApprovalRequest{Kind: "input", Detail: approvalDetail(payload)}
	if definition, ok := claudeDialogs[kind]; ok {
		r.Title = definition.title
		native := map[string]string{}
		for _, option := range definition.options {
			id, action := option.value, "submit"
			if option.value == "cancelled" || option.value == "cancel" || option.value == "not_now" {
				id, action = "deny", "cancel"
			}
			if option.value == "deny" || option.value == "decline" {
				id, action = "deny", "deny"
			}
			// A separate deny button is always available. Additional negative
			// options retain their own value in the split-button menu.
			for _, previous := range r.Decisions {
				if id == previous.ID {
					id = option.value
				}
			}
			r.Decisions = append(r.Decisions, chatmodel.ApprovalDecision{ID: id, Label: option.label, Kind: action})
			native[id] = option.value
		}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			choice, _ := r.Decision(response)
			return map[string]any{"behavior": "completed", "result": native[choice.ID]}
		}}, true
	}
	switch kind {
	case "auto_mode_outside_reads":
		r.Title = chatmodel.Clip(stringValue(payload["title"]), 240)
		r.Reason = chatmodel.Clip(stringValue(payload["question"])+"\n"+stringValue(payload["explainer"]), 8192)
		options, _ := payload["options"].([]any)
		for _, value := range options {
			option, _ := value.(map[string]any)
			id := stringValue(option["value"])
			if id != "allow" && id != "block" && id != "ask_again" && id != "allow_once" {
				return approvalProtocol{}, false
			}
			action := "submit"
			if id == "ask_again" {
				id, action = "deny", "deny"
			}
			r.Decisions = append(r.Decisions, chatmodel.ApprovalDecision{ID: id, Label: chatmodel.Clip(stringValue(option["label"]), 1024), Kind: action})
		}
		if len(r.Decisions) == 0 {
			return approvalProtocol{}, false
		}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			choice, _ := r.Decision(response)
			value := choice.ID
			if value == "deny" {
				value = "ask_again"
			}
			return map[string]any{"behavior": "completed", "result": value}
		}}, true
	case "sandbox_network_access":
		r.Title = "Network access"
		r.Reason = fmt.Sprint(payload["host"])
		r.Decisions = []chatmodel.ApprovalDecision{{ID: "deny", Label: "Deny", Kind: "deny"}, {ID: "cancel", Label: "Cancel request", Kind: "cancel"}, {ID: "allow", Label: "Allow once", Kind: "approve"}, {ID: "persist", Label: "Allow and save network rule", Kind: "approve"}}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			if response.Decision == "cancel" {
				return map[string]any{"behavior": "completed", "result": "cancelled"}
			}
			return map[string]any{"behavior": "completed", "result": map[string]any{"allow": response.Allow, "persistToSettings": response.Decision == "persist"}}
		}}, true
	case "auto_mode_flagged_allow":
		r.Title = "Review flagged permission rules"
		r.Decisions = []chatmodel.ApprovalDecision{{ID: "deny", Label: "Cancel request", Kind: "cancel"}, {ID: "allow", Label: "Apply selected removals", Kind: "submit"}}
		field := chatmodel.ApprovalField{Name: "remove", Label: "Rules to remove", Type: "array"}
		values, _ := payload["flagged"].([]any)
		for _, value := range values {
			if name := stringValue(value); name != "" {
				field.Options = append(field.Options, chatmodel.ApprovalOption{Value: name, Label: name})
			}
		}
		r.Fields = []chatmodel.ApprovalField{field}
		return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
			if !response.Allow {
				return map[string]any{"behavior": "cancelled"}
			}
			removed, _ := response.Content["remove"].([]any)
			if removed == nil {
				removed = []any{}
			}
			return map[string]any{"behavior": "completed", "result": map[string]any{"toRemove": removed}}
		}}, true
	}
	return approvalProtocol{}, false
}
