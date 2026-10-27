package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

type permissionCacheEntry struct {
	catalog chatmodel.PermissionCatalog
	until   time.Time
}

// Probe in the actual project, not the server's cwd. No inference or persistent
// settings writes are needed to discover these session-local capabilities.
func (r *CLIRunner) PermissionModes(ctx context.Context, backend chatmodel.Backend, directory string) (chatmodel.PermissionCatalog, error) {
	binary, err := r.Binary(backend)
	if err != nil {
		return chatmodel.PermissionCatalog{}, err
	}
	directory, err = WorkingDirectory(directory)
	if err != nil {
		return chatmodel.PermissionCatalog{}, err
	}
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	key := string(backend) + "\x00" + binary + "\x00" + directory
	if entry, ok := r.permissions[key]; ok && time.Now().Before(entry.until) {
		return entry.catalog, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return chatmodel.PermissionCatalog{}, fmt.Errorf("agent runner is closed")
	}
	r.permissionCancel = cancel
	r.processes.Add(1)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.permissionCancel = nil
		r.mu.Unlock()
		r.processes.Done()
	}()
	p, err := startAgentProcess(ctx, AgentRequest{Backend: backend, Binary: binary, WorkingDirectory: directory})
	if err != nil {
		return chatmodel.PermissionCatalog{}, err
	}
	defer func() { cancel(); <-p.done }()
	catalog := chatmodel.PermissionCatalog{Backend: backend, Default: chatmodel.DefaultPermissionMode(backend)}
	if backend == chatmodel.BackendClaude {
		if _, err = p.claudeControl(ctx, map[string]any{"subtype": "initialize", "hooks": nil}); err != nil {
			return catalog, err
		}
		for _, mode := range []string{"manual", "auto", "acceptEdits", "plan", "dontAsk", "bypassPermissions"} {
			response, e := p.claudeControl(ctx, map[string]any{"subtype": "set_permission_mode", "mode": mode})
			option := chatmodel.PermissionMode{ID: mode, Available: e == nil}
			if e == nil {
				applied := stringValue(response["mode"])
				if applied != "" && applied != mode && !(mode == "manual" && applied == "default") {
					option.Available = false
				}
			}
			if !option.Available {
				// Do not expose native diagnostics containing local policy paths.
				option.Reason = "Not available with this CLI version, account or project policy"
			}
			catalog.Modes = append(catalog.Modes, option)
		}
	} else {
		if err = p.initializeCodex(ctx); err != nil {
			return catalog, err
		}
		config, e := p.codexRPC(ctx, "config/read", map[string]any{"includeLayers": false, "cwd": directory})
		if e != nil {
			return catalog, e
		}
		requirements, e := p.codexRPC(ctx, "configRequirements/read", map[string]any{})
		if e != nil {
			return catalog, e
		}
		required, _ := requirements["requirements"].(map[string]any)
		autoEnabled := false
		cursor := ""
		for page := 0; page < 16; page++ {
			params := map[string]any{"limit": 100}
			if cursor != "" {
				params["cursor"] = cursor
			}
			flags, e := p.codexRPC(ctx, "experimentalFeature/list", params)
			if e != nil {
				break
			}
			items, _ := flags["data"].([]any)
			for _, item := range items {
				flag, _ := item.(map[string]any)
				if flag["name"] == "guardian_approval" {
					autoEnabled, _ = flag["enabled"].(bool)
				}
			}
			next := stringValue(flags["nextCursor"])
			if next == "" || next == cursor {
				break
			}
			cursor = next
		}
		if features, ok := required["featureRequirements"].(map[string]any); ok && features["guardian_approval"] == false {
			autoEnabled = false
		}
		catalog.Policy, catalog.Sandbox = "on-request", "workspace-write"
		for _, mode := range []string{"ask", "auto"} {
			reviewer := "user"
			if mode == "auto" {
				reviewer = "auto_review"
			}
			available := allowsValue(required, "allowedApprovalsReviewers", reviewer) &&
				allowsValue(required, "allowedApprovalPolicies", catalog.Policy) &&
				allowsValue(required, "allowedSandboxModes", catalog.Sandbox)
			option := chatmodel.PermissionMode{ID: mode, Available: available}
			if !available {
				option.Reason = "Restricted by project or administrator policy"
			}
			if mode == "auto" && !autoEnabled {
				option.Available = false
				option.Reason = "Not available with this CLI version, account or project policy"
			}
			catalog.Modes = append(catalog.Modes, option)
		}
		if nestedString(config, "config", "approvals_reviewer") == "auto_review" {
			catalog.Default = "auto"
		}
	}
	if catalog.Validate(catalog.Default) != nil {
		catalog.Default = ""
		for _, mode := range catalog.Modes {
			if mode.Available {
				catalog.Default = mode.ID
				break
			}
		}
	}
	catalog.Current = catalog.Default
	if r.permissions == nil {
		r.permissions = make(map[string]permissionCacheEntry)
	}
	// Bound cache growth when an administrator changes project directories.
	if len(r.permissions) >= 128 {
		clear(r.permissions)
	}
	r.permissions[key] = permissionCacheEntry{catalog, time.Now().Add(30 * time.Second)}
	return catalog, nil
}

func allowsValue(required map[string]any, key, value string) bool {
	values, ok := required[key].([]any)
	if !ok {
		return true
	}
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// Keep draining stdout while the owner decides, so cancellation and process
// exit cannot leave an unanswerable prompt or block the CLI's pipe.
func (p *agentProcess) awaitApproval(ctx context.Context, handler chatmodel.ApprovalHandler, request chatmodel.ApprovalRequest, cancelled func(map[string]any) bool) (bool, error) {
	if handler == nil {
		return false, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	type answer struct {
		allowed bool
		err     error
	}
	answerC := make(chan answer, 1)
	go func() { allowed, err := handler(ctx, request); answerC <- answer{allowed, err} }()
	for {
		select {
		case value := <-answerC:
			return value.allowed, value.err
		case <-ctx.Done():
			return false, contextError(ctx)
		case value, ok := <-p.messages:
			if !ok {
				return false, fmt.Errorf("agent exited while waiting for approval")
			}
			if cancelled(value) {
				return false, nil
			}
			if len(p.queued) >= 1024 {
				return false, fmt.Errorf("too many events while waiting for approval")
			}
			p.queued = append(p.queued, value)
			if value["type"] == "result" || value["method"] == "turn/completed" {
				return false, nil
			}
		}
	}
}

func approvalDetail(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return chatmodel.Clip(strings.TrimSpace(string(data)), 16*1024)
}
