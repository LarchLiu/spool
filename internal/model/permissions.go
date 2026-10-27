package model

import (
	"context"
	"fmt"
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
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ApprovalHandler func(context.Context, ApprovalRequest) (bool, error)
