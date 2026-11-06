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
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Detail      string          `json:"detail,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	URL         string          `json:"url,omitempty"`
	Fields      []ApprovalField `json:"fields,omitempty"`
	Unavailable string          `json:"unavailable,omitempty"`
}

type ApprovalResponse struct {
	Allow   bool           `json:"allow"`
	Content map[string]any `json:"content,omitempty"`
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
