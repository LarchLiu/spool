package agent

import (
	"context"
	"fmt"

	chatmodel "spool/internal/model"
)

// ForkNativeSession copies a Codex thread through its native protocol without
// starting a model turn. Claude's SDK slash catalog does not expose this action.
func (r *CLIRunner) ForkNativeSession(ctx context.Context, request AgentRequest) (string, error) {
	if request.Backend != chatmodel.BackendCodex || !chatmodel.ValidID(request.NativeSessionID) {
		return "", fmt.Errorf("native session forking is unavailable")
	}
	directory, err := WorkingDirectory(request.WorkingDirectory)
	if err != nil {
		return "", err
	}
	request.WorkingDirectory, request.ChatID = directory, chatmodel.NewID()
	chat, err := r.reserve(request)
	if err != nil {
		return "", err
	}
	defer r.release(request.ChatID, chat, false)
	stop := context.AfterFunc(ctx, chat.cancel)
	defer stop()
	if err := r.startReservedProcess(chat, request); err != nil {
		return "", err
	}
	p := chat.process
	if err := p.initializeCodex(ctx); err != nil {
		return "", err
	}
	params := map[string]any{"threadId": request.NativeSessionID, "excludeTurns": true, "deferGoalContinuation": true}
	if request.Model != "" {
		params["model"] = request.Model
	}
	response, err := p.codexRPC(ctx, "thread/fork", params)
	if err != nil {
		return "", err
	}
	id := nestedString(response, "thread", "id")
	if !chatmodel.ValidID(id) || id == request.NativeSessionID {
		return "", fmt.Errorf("Codex returned an invalid fork ID")
	}
	return id, nil
}
