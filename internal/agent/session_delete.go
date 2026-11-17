package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	chatmodel "spool/internal/model"
)

// SessionDeleter permanently removes provider-owned session history.
type SessionDeleter interface {
	DeleteSession(context.Context, AgentRequest) error
}

func (r *CLIRunner) DeleteSession(ctx context.Context, request AgentRequest) error {
	if !chatmodel.ValidID(request.NativeSessionID) {
		return errors.New("invalid session deletion")
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
		history := &CodexHistory{Binary: binary}
		defer history.Close()
		_, err := history.call(ctx, "thread/delete", map[string]any{"threadId": request.NativeSessionID})
		return err
	case chatmodel.BackendClaude:
		return deleteClaudeSession(ctx, request.NativeSessionID)
	default:
		return errors.New("unsupported agent backend")
	}
}

// Match the local deleteSession contract of Claude Agent SDK 0.3.326:
// remove the first nonempty transcript and its session/subagent directory.
func deleteClaudeSession(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		return errors.New("invalid Claude session ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := nativeSessionHome(chatmodel.BackendClaude)
	if err != nil {
		return err
	}
	root := filepath.Join(home, "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if !dir.IsDir() && dir.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(root, dir.Name(), id+".jsonl")
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.RemoveAll(filepath.Join(root, dir.Name(), id))
	}
	return os.ErrNotExist
}
