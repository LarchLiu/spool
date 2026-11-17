package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"

	chatmodel "spool/internal/model"
)

// SessionRenamer changes provider-owned metadata without sending a model prompt.
type SessionRenamer interface {
	RenameSession(context.Context, AgentRequest, string) error
}

func (r *CLIRunner) RenameSession(ctx context.Context, request AgentRequest, title string) error {
	if strings.TrimSpace(title) == "" || !chatmodel.ValidID(request.NativeSessionID) {
		return errors.New("invalid session rename")
	}
	switch request.Backend {
	case chatmodel.BackendCodex:
		binary := request.Binary
		if binary == "" {
			var err error
			binary, err = r.Binary(request.Backend)
			if err != nil {
				return err
			}
		}
		// Use a separate metadata connection so an in-flight turn keeps its
		// event stream. No thread resume or model turn is needed.
		history := &CodexHistory{Binary: binary}
		defer history.Close()
		return history.Rename(ctx, request.NativeSessionID, title)
	case chatmodel.BackendClaude:
		return renameClaudeSession(ctx, request, title)
	default:
		return errors.New("unsupported agent backend")
	}
}

// Match the local-filesystem renameSession contract of Claude Agent SDK
// 0.3.323: append one custom-title record to an existing, nonempty transcript.
func renameClaudeSession(ctx context.Context, request AgentRequest, title string) error {
	if _, err := uuid.Parse(request.NativeSessionID); err != nil {
		return errors.New("invalid Claude session ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path := request.NativeSessionPath
	if path == "" {
		var err error
		path, err = FindSessionTranscript(chatmodel.BackendClaude, request.NativeSessionID, request.WorkingDirectory)
		if err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("Claude session transcript is unavailable")
	}
	return json.NewEncoder(file).Encode(map[string]string{
		"type": "custom-title", "customTitle": title, "sessionId": request.NativeSessionID,
	})
}

// Rename changes a stored Codex thread's name without loading its conversation.
func (h *CodexHistory) Rename(ctx context.Context, threadID, title string) error {
	if !chatmodel.ValidID(threadID) || strings.TrimSpace(title) == "" {
		return errors.New("invalid Codex session rename")
	}
	_, err := h.call(ctx, "thread/name/set", map[string]any{"threadId": threadID, "name": title})
	return err
}
