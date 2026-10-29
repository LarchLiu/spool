package workspace

import (
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
func (s *Store) forkChat(id string, backend chatmodel.Backend, options chatmodel.CodexOptions) (chatmodel.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.getSession(id)
	if !ok {
		return source, os.ErrNotExist
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
	project, err := s.Project(source.ProjectID)
	if err != nil {
		return source, err
	}
	path := source.NativeSessionPath
	if path == "" {
		path, err = agent.FindSessionTranscript(source.Backend, source.NativeSessionID, project.Path)
	}
	if err != nil {
		return source, errSessionConversion
	}
	converted, err := agent.ConvertSession(source.Backend, backend, path, project.Path)
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
	tx, err := s.db.Begin()
	if err != nil {
		return chat, err
	}
	defer tx.Rollback()
	if err = insertSession(tx, chat); err != nil {
		return chat, err
	}
	if err = tx.Commit(); err != nil {
		return chat, err
	}
	committed = true
	chat, _ = s.Get(chat.ID)
	return chat, nil
}

func (a *App) forkChat(id string, backend chatmodel.Backend, options chatmodel.CodexOptions) (chatmodel.Session, error) {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closing {
		return chatmodel.Session{}, fmt.Errorf("agent service is stopping")
	}
	if a.updating {
		return chatmodel.Session{}, ErrUpdateInProgress
	}
	if a.nativeCommandRunning(id) {
		return chatmodel.Session{}, fmt.Errorf("this chat already has a running turn")
	}
	return a.store.forkChat(id, backend, options)
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
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id=?`, chat.ID); err != nil {
		return
	}
	if tx.Commit() == nil {
		_ = os.Remove(chat.NativeSessionPath)
	}
}
