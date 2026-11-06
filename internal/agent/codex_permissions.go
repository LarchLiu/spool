package agent

import (
	"fmt"
	"strings"

	chatmodel "spool/internal/model"
)

// Each checkbox refers to an unchanged value from the native request. Deny
// entries accompany any selected filesystem grants, preserving exclusions.
func codexPermissionApproval(params map[string]any) approvalProtocol {
	profile, ok := params["permissions"].(map[string]any)
	r := chatmodel.ApprovalRequest{Title: "Additional permissions", Reason: chatmodel.Clip(stringValue(params["reason"]), 8192), Detail: approvalDetail(profile),
		Decisions: []chatmodel.ApprovalDecision{{ID: "deny", Label: "Deny", Kind: "deny"}, {ID: "allow", Label: "Allow for this turn", Kind: "approve"}, {ID: "session", Label: "Allow for this session", Kind: "approve"}}}
	if !ok || !schemaKeys(profile, "network", "fileSystem") {
		r.Unavailable = "This request contains unsupported permissions."
	}
	type grant struct {
		group string
		value any
	}
	grants := map[string]grant{}
	field := chatmodel.ApprovalField{Name: "permissions", Label: "Requested permissions", Type: "array"}
	defaults := []any{}
	add := func(group, label string, value any) {
		id := fmt.Sprintf("grant-%d", len(grants))
		grants[id] = grant{group, value}
		field.Options = append(field.Options, chatmodel.ApprovalOption{Value: id, Label: label})
		defaults = append(defaults, id)
	}
	if network, ok := profile["network"].(map[string]any); ok {
		if !schemaKeys(network, "enabled") {
			r.Unavailable = "This request contains unsupported permissions."
		}
		if network["enabled"] == true {
			add("network", "Network access", network)
		}
	}
	fs, _ := profile["fileSystem"].(map[string]any)
	if !schemaKeys(fs, "read", "write", "entries", "globScanMaxDepth") {
		r.Unavailable = "This request contains unsupported permissions."
	}
	for _, key := range []string{"read", "write", "entries"} {
		values, _ := fs[key].([]any)
		for _, value := range values {
			label := key + ": " + fmt.Sprint(value)
			if key == "entries" {
				entry, _ := value.(map[string]any)
				if entry["access"] == "deny" {
					continue
				}
				if entry["access"] != "read" && entry["access"] != "write" {
					r.Unavailable = "This request contains unsupported permissions."
					continue
				}
				label = stringValue(entry["access"]) + ": " + approvalDetail(entry["path"])
			}
			add(key, strings.TrimSpace(label), value)
		}
	}
	field.Default = defaults
	if len(field.Options) > 0 {
		r.Fields = append(r.Fields, field)
	}
	r.Fields = append(r.Fields, chatmodel.ApprovalField{Name: "strict_auto_review", Label: "Require strict automatic review", Type: "boolean"})
	return approvalProtocol{r, func(response chatmodel.ApprovalResponse) map[string]any {
		permissions := map[string]any{}
		choice, _ := r.Decision(response)
		scope := "turn"
		if choice.ID == "session" {
			scope = "session"
		}
		if response.Allow {
			selected, exists := response.Content["permissions"].([]any)
			if !exists {
				selected = defaults
			}
			fileSystem := map[string]any{}
			for _, option := range field.Options {
				for _, id := range selected {
					if id != option.Value {
						continue
					}
					g := grants[option.Value]
					if g.group == "network" {
						permissions["network"] = g.value
						continue
					}
					values, _ := fileSystem[g.group].([]any)
					fileSystem[g.group] = append(values, g.value)
				}
			}
			if len(fileSystem) > 0 {
				entries, _ := fs["entries"].([]any)
				for _, value := range entries {
					entry, _ := value.(map[string]any)
					if entry["access"] == "deny" {
						values, _ := fileSystem["entries"].([]any)
						fileSystem["entries"] = append(values, value)
					}
				}
				if fs["globScanMaxDepth"] != nil {
					fileSystem["globScanMaxDepth"] = fs["globScanMaxDepth"]
				}
				permissions["fileSystem"] = fileSystem
			}
		}
		result := map[string]any{"permissions": permissions, "scope": scope}
		if response.Allow && response.Content["strict_auto_review"] == true {
			result["strictAutoReview"] = true
		}
		return result
	}}
}
