package model

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type PermissionMode struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type PermissionCatalog struct {
	Backend Backend          `json:"backend"`
	Default string           `json:"default"`
	Current string           `json:"current"`
	Modes   []PermissionMode `json:"modes"`
	Policy  string           `json:"approval_policy,omitempty"`
	Sandbox string           `json:"sandbox,omitempty"`
}

func DefaultPermissionMode(backend Backend) string {
	if backend == BackendClaude {
		return "acceptEdits"
	}
	return "ask"
}

func ValidatePermissionMode(backend Backend, mode string) error {
	if mode == "" {
		return nil
	}
	if backend == BackendCodex && (mode == "ask" || mode == "auto") {
		return nil
	}
	if backend == BackendClaude {
		switch mode {
		case "manual", "auto", "acceptEdits", "plan", "dontAsk", "bypassPermissions":
			return nil
		}
	}
	return fmt.Errorf("Invalid permission mode")
}

func (c PermissionCatalog) Validate(mode string) error {
	for _, item := range c.Modes {
		if item.ID == mode && item.Available {
			return nil
		}
	}
	return fmt.Errorf("Permission mode is unavailable for this project")
}

type ApprovalRequest struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Detail      string             `json:"detail,omitempty"`
	Reason      string             `json:"reason,omitempty"`
	URL         string             `json:"url,omitempty"`
	Fields      []ApprovalField    `json:"fields,omitempty"`
	Kind        string             `json:"kind,omitempty"`
	Decisions   []ApprovalDecision `json:"decisions,omitempty"`
	Schema      map[string]any     `json:"schema,omitempty"`
	JSONForm    bool               `json:"json_form,omitempty"`
	Feedback    bool               `json:"feedback,omitempty"`
	Unavailable string             `json:"unavailable,omitempty"`
}

// IDs refer only to choices retained with the pending server request. Clients
// never send native policy objects or invent a permission scope.
type ApprovalDecision struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"` // approve, deny, cancel, or submit
}

type ApprovalResponse struct {
	Allow    bool           `json:"allow"`
	Decision string         `json:"decision,omitempty"`
	Content  map[string]any `json:"content,omitempty"`
	Feedback string         `json:"feedback,omitempty"`
}

func (r ApprovalRequest) Decision(response ApprovalResponse) (ApprovalDecision, error) {
	id := response.Decision
	if id == "" {
		id = "deny"
		if response.Allow {
			id = "allow"
		}
	}
	if len(r.Decisions) == 0 {
		if id == "allow" {
			return ApprovalDecision{ID: id, Kind: "approve"}, nil
		}
		if id == "deny" {
			return ApprovalDecision{ID: id, Kind: "deny"}, nil
		}
	}
	for _, decision := range r.Decisions {
		if decision.ID == id {
			return decision, nil
		}
	}
	return ApprovalDecision{}, fmt.Errorf("This decision is not available for this request")
}

func (r ApprovalRequest) NormalizeResponse(response ApprovalResponse) (ApprovalResponse, error) {
	if err := r.ValidateResponse(response); err != nil {
		return ApprovalResponse{}, err
	}
	decision, _ := r.Decision(response)
	response.Allow = decision.Kind == "approve" || decision.Kind == "submit"
	if len(r.Decisions) != 0 {
		response.Decision = decision.ID
	}
	if !response.Allow {
		response.Content = nil
	}
	return response, nil
}

func (r ApprovalRequest) ResponseAudit(response ApprovalResponse) (string, string) {
	decision, _ := r.Decision(response)
	kind := decision.Kind
	if kind == "submit" {
		kind = "respond"
	}
	summary := r.AuditSummary()
	if r.Title == "Additional permissions" && (decision.Kind == "approve") {
		for _, field := range r.Fields {
			if field.Name != "permissions" {
				continue
			}
			values, exists := response.Content[field.Name].([]any)
			if !exists {
				values, _ = field.Default.([]any)
			}
			labels := []string{}
			for _, option := range field.Options {
				for _, value := range values {
					if value == option.Value {
						labels = append(labels, option.Label)
					}
				}
			}
			if len(labels) == 0 {
				labels = append(labels, "No permissions granted")
			}
			summary = (ApprovalRequest{Title: r.Title, Reason: strings.Join(labels, "; ")}).AuditSummary()
		}
	}
	if decision.Label != "" {
		label := (ApprovalRequest{Title: decision.Label}).AuditSummary()
		if runes := []rune(label); len(runes) > 96 {
			label = string(runes[:95]) + "…"
		}
		budget := 200 - len([]rune(label)) - 3
		if runes := []rune(summary); len(runes) > budget {
			summary = string(runes[:budget-1]) + "…"
		}
		summary = ApprovalRequest{Title: summary, Reason: label}.AuditSummary()
	}
	return kind, summary
}

// AuditSummary describes the request, without persisting submitted form values
// or an authorization URL. Command previews identify the exact approved command.
func (r ApprovalRequest) AuditSummary() string {
	detail := r.Reason
	if detail == "" {
		detail = r.Detail
	}
	if r.Title == "Command execution" {
		detail = strings.SplitN(r.Detail, "\n", 2)[0]
	}
	clean := func(value string) string {
		return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	}
	title, detail := clean(r.Title), clean(detail)
	text := title
	if detail != "" && detail != title {
		if text != "" {
			text += " · "
		}
		text += detail
	}
	if chars := []rune(text); len(chars) > 200 {
		return string(chars[:199]) + "…"
	}
	return text
}

type ApprovalHandler func(context.Context, ApprovalRequest) (ApprovalResponse, error)
