package agent

import (
	"context"

	chatmodel "spool/internal/model"
)

// CodexModelProvider reads the effective project provider without creating a
// thread or sending a model request. It honors the runner's configured binary.
func (r *CLIRunner) CodexModelProvider(ctx context.Context, directory string) (string, error) {
	binary, err := r.Binary(chatmodel.BackendCodex)
	if err != nil {
		return "", err
	}
	directory, err = WorkingDirectory(directory)
	if err != nil {
		return "", err
	}
	request := AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, WorkingDirectory: directory, ChatID: chatmodel.NewID()}
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
	if err := chat.process.initializeCodex(ctx); err != nil {
		return "", err
	}
	return chat.process.codexModelProvider(ctx, directory)
}
