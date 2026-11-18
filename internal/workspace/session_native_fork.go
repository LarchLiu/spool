package workspace

import (
	"context"
	"fmt"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func (a *App) forkNativeChat(ctx context.Context, source chatmodel.Session, options chatmodel.CodexOptions, user string) (chatmodel.Session, error) {
	a.turnMu.Lock()
	if a.closing || a.updating || a.activeTurns[source.ID] != nil {
		a.turnMu.Unlock()
		return chatmodel.Session{}, fmt.Errorf("this chat already has a running turn")
	}
	latest, ok := a.store.Get(source.ID)
	if !ok || latest.Status == chatmodel.StatusRunning || latest.AutomationChat || a.store.nativeSessionArchived(latest.Backend, latest.NativeSessionID) {
		a.turnMu.Unlock()
		return chatmodel.Session{}, fmt.Errorf("this session cannot be forked")
	}
	source = latest
	ctx, control := a.registerTurn(ctx, source.ID, "command:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(source.ID, control)
	provider, ok := a.runner.(interface {
		ForkNativeSession(context.Context, agent.AgentRequest) (string, error)
	})
	if !ok {
		return chatmodel.Session{}, fmt.Errorf("native session forking is unavailable")
	}
	directory, err := a.store.sessionDirectory(source)
	if err != nil {
		return chatmodel.Session{}, err
	}
	binary, err := a.runner.Binary(source.Backend)
	if err != nil {
		return chatmodel.Session{}, err
	}
	native, err := provider.ForkNativeSession(ctx, agent.AgentRequest{Backend: source.Backend, Binary: binary, WorkingDirectory: directory, NativeSessionID: source.NativeSessionID, Model: options.Model})
	if err != nil {
		return chatmodel.Session{}, err
	}
	now := time.Now().UnixMilli()
	chat := chatmodel.Session{ID: chatmodel.NewID(), UserID: user, ProjectID: source.ProjectID, Title: source.Title, Backend: source.Backend, NativeSessionID: native, Model: options.Model, ReasoningEffort: options.ReasoningEffort, PermissionMode: source.PermissionMode, Status: chatmodel.StatusReady, CreatedAt: now, UpdatedAt: now}
	tx, err := a.store.db.Begin()
	if err != nil {
		return chat, err
	}
	defer tx.Rollback()
	if err := insertSession(tx, chat); err != nil {
		return chat, err
	}
	if _, err := tx.Exec(`INSERT INTO session_workspaces(session_id,directory) VALUES(?,?)`, chat.ID, directory); err != nil {
		return chat, err
	}
	if err := a.store.notify(tx.Commit(), changeNavigation); err != nil {
		return chat, err
	}
	chat, _ = a.store.Get(chat.ID)
	return chat, nil
}
