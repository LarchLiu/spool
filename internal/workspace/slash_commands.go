package workspace

import (
	"fmt"
	"net/http"
	"strings"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Native local commands have no conversation turn. Reserve the same session
// slot as messages, but return their output directly instead of inventing a
// model response or a native-history entry.
func (a *App) agentCommandResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	var input struct {
		Command        string  `json:"command"`
		Model          *string `json:"model"`
		Effort         *string `json:"reasoning_effort"`
		PermissionMode *string `json:"permission_mode"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	command := chatmodel.ParseSlashCommand(input.Command)
	if command == nil || len(input.Command) > chatmodel.MaxMessageBytes || !agent.IsControlCommand(chat.Backend, command.Name) {
		apiError(w, 400, "Invalid native control command")
		return
	}
	if command.Name == "plan" && command.Arguments != "" || command.Name == "goal" {
		apiError(w, 400, "Use the goal or message endpoint for this command")
		return
	}
	a.turnMu.Lock()
	latest, ok := a.store.Get(chat.ID)
	if !ok || a.closing || a.updating || latest.Status == chatmodel.StatusRunning || a.activeTurns[chat.ID] != nil || a.store.nativeSessionArchived(latest.Backend, latest.NativeSessionID) {
		a.turnMu.Unlock()
		apiError(w, 409, "Wait for the running response before using agent commands")
		return
	}
	chat = latest
	if input.Model != nil {
		chat.Model = *input.Model
	}
	if input.Effort != nil {
		chat.ReasoningEffort = *input.Effort
	}
	if input.PermissionMode != nil {
		chat.PermissionMode = *input.PermissionMode
	}
	if chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: chat.Model, ReasoningEffort: chat.ReasoningEffort}) != nil || chatmodel.ValidatePermissionMode(chat.Backend, chat.PermissionMode) != nil {
		a.turnMu.Unlock()
		apiError(w, 400, "Invalid agent settings")
		return
	}
	if !a.enabled(chat.Backend) {
		a.turnMu.Unlock()
		apiError(w, 409, "agent 未启用或尚未安装")
		return
	}
	ctx, control := a.registerTurn(r.Context(), chat.ID, "command:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(chat.ID, control)
	if err := a.validateSelection(ctx, chat, chatmodel.CodexOptions{Model: chat.Model, ReasoningEffort: chat.ReasoningEffort}); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if input.PermissionMode != nil && *input.PermissionMode != "" {
		catalog, err := a.permissionCatalog(ctx, chat)
		if err != nil {
			permissionDiscoveryUnavailable(w)
			return
		}
		if err := catalog.Validate(*input.PermissionMode); err != nil {
			apiError(w, 400, err.Error())
			return
		}
	}
	directory, err := a.store.sessionDirectory(chat)
	if err != nil {
		apiError(w, 500, "project directory is unavailable")
		return
	}
	binary, err := a.runner.Binary(chat.Backend)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	result, err := a.runner.Run(ctx, agent.AgentRequest{
		ChatID: chat.ID, Backend: chat.Backend, Binary: binary, WorkingDirectory: directory,
		NativeSessionID: chat.NativeSessionID, NativeSessionPath: chat.NativeSessionPath,
		Prompt: strings.TrimSpace(input.Command), Model: chat.Model, ReasoningEffort: chat.ReasoningEffort, PermissionMode: chat.PermissionMode,
	}, nil)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	if result.SessionID != "" && result.SessionID != chat.NativeSessionID {
		if err := a.store.SetNativeSession(chat.ID, result.SessionID); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	}
	// Native commands may change settings without emitting ordinary turn events.
	if result.Model == "" {
		result.Model = chat.Model
	}
	// Claude can select custom model IDs that its discovery catalog does not
	// describe. Keep their native effort at its default rather than persisting
	// an explicit level the model picker cannot subsequently submit.
	if chat.Backend == chatmodel.BackendClaude && result.ReasoningEffort != "" {
		if err := a.validateSelection(ctx, chat, chatmodel.CodexOptions{Model: result.Model, ReasoningEffort: result.ReasoningEffort}); err != nil {
			result.ReasoningEffort = ""
		}
	}
	if result.PermissionMode == "" {
		result.PermissionMode = chat.PermissionMode
	}
	if result.PermissionMode != "" && result.PermissionMode != latest.PermissionMode {
		if err := a.store.saveProjectPermissionMode(chat.ProjectID, chat.Backend, result.PermissionMode); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	}
	if err := a.store.saveCommandSettings(chat.ID, result); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	apiJSON(w, 200, map[string]any{"message": result.Text, "model": result.Model, "reasoning_effort": result.ReasoningEffort, "permission_mode": result.PermissionMode})
}

func (s *Store) saveCommandSettings(id string, result agent.AgentResult) error {
	if result.Model != "" {
		if err := chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: result.Model, ReasoningEffort: result.ReasoningEffort}); err != nil {
			return fmt.Errorf("invalid native settings: %w", err)
		}
	}
	_, err := s.db.Exec(`UPDATE sessions SET model=?,reasoning_effort=?,permission_mode=? WHERE id=?`, result.Model, result.ReasoningEffort, result.PermissionMode, id)
	return s.notify(err, changeNavigation)
}
