package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

var errTaskSessionConversion = errors.New("Task chats cannot switch agents. Continue in the original chat to preserve its issue claim.")

// A conversion is a new native session and a new chat, not a link to a mutable
// source. Subsequent messages on either side cannot alter the other's context.
func (s *Store) forkChat(ctx context.Context, id string, backend chatmodel.Backend, options chatmodel.CodexOptions, owner ...string) (chatmodel.Session, error) {
	chat, err := s.createFork(ctx, id, backend, options, owner...)
	if err != nil {
		return chat, err
	}
	// Reading the new session's history can wait for its provider, so it is
	// read after the store lock is released.
	chat, _ = s.Get(chat.ID)
	return chat, nil
}

func (s *Store) createFork(ctx context.Context, id string, backend chatmodel.Backend, options chatmodel.CodexOptions, owner ...string) (chatmodel.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.getSession(id)
	if !ok {
		return source, os.ErrNotExist
	}
	if s.nativeSessionArchived(source.Backend, source.NativeSessionID) {
		return source, fmt.Errorf("This chat is archived")
	}
	if len(owner) > 0 && !s.CanCreateChat(owner[0], source.ProjectID) {
		return source, os.ErrPermission
	}
	if source.Status == chatmodel.StatusRunning {
		return source, fmt.Errorf("this chat already has a running turn")
	}
	if !backend.Valid() || backend == source.Backend {
		return source, fmt.Errorf("unsupported session conversion")
	}
	if source.AutomationChat {
		return source, errTaskSessionConversion
	}
	if err := chatmodel.ValidateCodexOptions(options); err != nil {
		return source, err
	}
	if backend == chatmodel.BackendClaude {
		if err := chatmodel.ValidateClaudeEffort(options.ReasoningEffort); err != nil {
			return source, err
		}
	}
	directory, err := s.sessionDirectory(source)
	if err != nil {
		return source, err
	}
	path := source.NativeSessionPath
	if path == "" {
		path, err = agent.FindSessionTranscript(source.Backend, source.NativeSessionID, directory)
	}
	if err != nil {
		return source, errSessionConversion
	}
	provider := ""
	if backend == chatmodel.BackendCodex {
		all, err := s.Agents()
		if err != nil {
			return source, err
		}
		runner := agent.NewCLIRunner()
		runner.ConfigureBinaries(agentBinaries(all))
		provider, err = runner.CodexModelProvider(ctx, directory)
		_ = runner.Close()
		if err != nil {
			return source, errSessionConversion
		}
	}
	if err := ctx.Err(); err != nil {
		return source, err
	}
	converted, err := agent.ConvertSession(source.Backend, backend, path, directory, provider)
	if err != nil {
		return source, errSessionConversion
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(converted.Path)
		}
	}()
	now := time.Now().UnixMilli()
	chat := chatmodel.Session{ID: chatmodel.NewID(), UserID: source.UserID, ProjectID: source.ProjectID, Title: source.Title, Backend: backend, NativeSessionID: converted.ID, NativeSessionPath: converted.Path, Model: options.Model, ReasoningEffort: options.ReasoningEffort, PermissionMode: chatmodel.DefaultPermissionMode(backend), CreatedAt: now, UpdatedAt: now, Status: chatmodel.StatusReady}
	if len(owner) > 0 {
		chat.UserID = owner[0]
	}
	tx, err := s.db.Begin()
	if err != nil {
		return chat, err
	}
	defer tx.Rollback()
	if err = insertSession(tx, chat); err != nil {
		return chat, err
	}
	if _, err = tx.Exec(`INSERT INTO session_workspaces(session_id,directory) VALUES(?,?)`, chat.ID, directory); err != nil {
		return chat, err
	}
	if err = tx.Commit(); err != nil {
		return chat, err
	}
	committed = true
	s.changes.publish(changeNavigation)
	return chat, nil
}

func (a *App) forkChat(ctx context.Context, id string, backend chatmodel.Backend, options chatmodel.CodexOptions, owner ...string) (chatmodel.Session, error) {
	a.turnMu.Lock()
	if a.closing {
		a.turnMu.Unlock()
		return chatmodel.Session{}, fmt.Errorf("agent service is stopping")
	}
	if a.updating {
		a.turnMu.Unlock()
		return chatmodel.Session{}, ErrUpdateInProgress
	}
	if a.activeTurns[id] != nil {
		a.turnMu.Unlock()
		return chatmodel.Session{}, fmt.Errorf("this chat already has a running turn")
	}
	ctx, control := a.registerTurn(ctx, id, "command:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(id, control)
	return a.store.forkChat(ctx, id, backend, options, owner...)
}

// Only for a newly created conversion whose first request failed admission.
func (s *Store) discardFork(chat chatmodel.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var status chatmodel.SessionStatus
	if tx.QueryRow(`SELECT status FROM sessions WHERE id=? AND native_session_id=?`, chat.ID, chat.NativeSessionID).Scan(&status) != nil || status != chatmodel.StatusReady {
		return
	}
	if _, err = tx.Exec(`DELETE FROM chat_collaborators WHERE session_id=?`, chat.ID); err != nil {
		return
	}
	if _, err = tx.Exec(`DELETE FROM automation_chats WHERE session_id=?`, chat.ID); err != nil {
		return
	}
	if _, err = tx.Exec(`DELETE FROM session_workspaces WHERE session_id=?`, chat.ID); err != nil {
		return
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id=?`, chat.ID); err != nil {
		return
	}
	if tx.Commit() == nil {
		s.changes.publish(changeNavigation)
		_ = os.Remove(chat.NativeSessionPath)
	}
}
