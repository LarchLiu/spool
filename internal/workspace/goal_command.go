package workspace

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func validateGoalInput(input messageInput) error {
	command, err := chatmodel.ParseGoalCommand(input.Message)
	if err != nil {
		return err
	}
	if command == nil {
		return nil
	}
	if len(input.files) > 0 {
		return fmt.Errorf("Goal commands do not accept attachments")
	}
	if command.Action != "set" {
		return fmt.Errorf("Use the native goal control endpoint to view or clear a goal")
	}
	return nil
}

func (a *App) goalCommandResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	var input struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Action != "get" && input.Action != "clear" {
		apiError(w, 400, "Invalid goal command")
		return
	}
	a.turnMu.Lock()
	latest, ok := a.store.Get(chat.ID)
	if !ok {
		a.turnMu.Unlock()
		apiError(w, 404, "聊天不存在")
		return
	}
	chat = latest
	if a.closing || a.updating || chat.Status == chatmodel.StatusRunning || a.activeTurns[chat.ID] != nil {
		a.turnMu.Unlock()
		apiError(w, 409, "Wait for the running response before using goal commands")
		return
	}
	if !a.enabled(chat.Backend) {
		a.turnMu.Unlock()
		apiError(w, 409, "agent 未启用或尚未安装")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx, control := a.registerTurn(ctx, chat.ID, "goal:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(chat.ID, control)
	if chat.NativeSessionID == "" {
		apiJSON(w, 200, map[string]string{"message": "No goal set."})
		return
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		apiError(w, 500, "project directory is unavailable")
		return
	}
	binary, err := a.runner.Binary(chat.Backend)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	prompt := "/goal"
	if input.Action == "clear" {
		prompt += " clear"
	}
	result, err := a.runner.Run(ctx, agent.AgentRequest{
		ChatID: chat.ID, Backend: chat.Backend, Binary: binary, WorkingDirectory: project.Path,
		NativeSessionID: chat.NativeSessionID, NativeSessionPath: chat.NativeSessionPath,
		Prompt: prompt, Model: chat.Model, ReasoningEffort: chat.ReasoningEffort, PermissionMode: chat.PermissionMode,
	}, nil)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	apiJSON(w, 200, map[string]string{"message": result.Text})
}

func (a *App) nativeCommandRunning(chatID string) bool {
	control := a.activeTurns[chatID]
	return control != nil && strings.HasPrefix(control.id, "goal:")
}
