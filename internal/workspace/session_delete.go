package workspace

import (
	"context"
	"net/http"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func (a *App) deleteChatResponse(w http.ResponseWriter, r *http.Request, user string, chat chatmodel.Session) {
	a.metadataMu.Lock()
	defer a.metadataMu.Unlock()
	deleter, ok := a.runner.(agent.SessionDeleter)
	if !ok {
		apiError(w, 503, "Could not delete chat")
		return
	}
	a.turnMu.Lock()
	chat, ok = a.store.getSession(chat.ID)
	if !ok || !a.store.CanControlChat(user, chat.ID) {
		a.turnMu.Unlock()
		apiError(w, 404, "聊天不存在")
		return
	}
	if a.closing || a.updating || chat.Status == chatmodel.StatusRunning || a.activeTurns[chat.ID] != nil {
		a.turnMu.Unlock()
		apiError(w, 409, "Wait for the running response before deleting this chat")
		return
	}
	if len(a.queuedMessages(chat.ID)) > 0 {
		a.turnMu.Unlock()
		apiError(w, 409, "Remove queued messages before deleting this chat")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx, control := a.registerTurn(ctx, chat.ID, "delete:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(chat.ID, control)
	if chat.NativeSessionID != "" {
		if err := deleter.DeleteSession(ctx, agent.AgentRequest{
			ChatID: chat.ID, Backend: chat.Backend,
			NativeSessionID: chat.NativeSessionID, NativeSessionPath: chat.NativeSessionPath,
		}); err != nil {
			apiError(w, 502, "Could not delete chat")
			return
		}
	}
	refreshErr := a.store.syncLocalSessionsContext(ctx, true)
	a.store.localScanMu.Lock()
	listed := chat.NativeSessionID == "" || a.store.localIndex.ListingSucceeded(chat.Backend)
	a.store.localScanMu.Unlock()
	if err := a.store.removeDeletedSession(chat); err != nil {
		apiError(w, 500, "Chat was deleted, but Spool could not remove its local records")
		return
	}
	a.liveMu.Lock()
	delete(a.completed, chat.ID)
	a.liveMu.Unlock()
	if refreshErr != nil || !listed {
		apiError(w, 502, "Chat was deleted, but the native session list could not be refreshed")
		return
	}
	navigation, err := a.store.UserNavigation(user)
	if err != nil {
		apiError(w, 502, "Chat was deleted, but the native session list could not be refreshed")
		return
	}
	apiJSON(w, 200, navigation)
}

func (s *Store) removeDeletedSession(chat chatmodel.Session) error {
	s.localScanMu.Lock()
	defer s.localScanMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"local_session_files", "local_sessions", "session_workspaces", "chat_collaborators", "chat_artifacts", "automation_chats"} {
		if _, err = tx.Exec(`DELETE FROM `+table+` WHERE session_id=?`, chat.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE project_automations SET last_chat_id='' WHERE last_chat_id=?`, chat.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE automation_runs SET chat_id='' WHERE chat_id=?`, chat.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id=?`, chat.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM spool_native_sessions WHERE backend=? AND native_session_id=?`, chat.Backend, chat.NativeSessionID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.localIndex.Forget(chat.Backend, chat.NativeSessionID)
	s.nativeMu.Lock()
	delete(s.nativeSessions, string(chat.Backend)+":"+chat.NativeSessionID)
	s.nativeMu.Unlock()
	s.runtimeMu.Lock()
	delete(s.runtimeTurns, chat.ID)
	s.runtimeMu.Unlock()
	s.historyCache.forget(chat.Backend, chat.NativeSessionPath)
	if chat.Backend == chatmodel.BackendCodex {
		s.codexHistory.forget(chat.NativeSessionID)
	}
	s.changes.publish(changeNavigation)
	return nil
}
