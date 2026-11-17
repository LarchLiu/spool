package workspace

import (
	"context"
	"net/http"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func (a *App) archiveChatResponse(w http.ResponseWriter, r *http.Request, user string, chat chatmodel.Session) {
	a.metadataMu.Lock()
	defer a.metadataMu.Unlock()
	archiver, ok := a.runner.(agent.SessionArchiver)
	if !ok {
		apiError(w, 503, "Could not archive chat")
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
		apiError(w, 409, "Wait for the running response before archiving this chat")
		return
	}
	if len(a.queuedMessages(chat.ID)) > 0 {
		a.turnMu.Unlock()
		apiError(w, 409, "Remove queued messages before archiving this chat")
		return
	}
	if chat.NativeSessionID == "" {
		a.turnMu.Unlock()
		apiError(w, 409, "Send a message before archiving this chat")
		return
	}
	directory, err := a.store.sessionDirectory(chat)
	if err != nil {
		a.turnMu.Unlock()
		apiError(w, 409, "project directory is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// Reserve only this chat while native I/O runs. Turn admission, conversion,
	// shutdown and updates use the same lifecycle as other native commands.
	ctx, control := a.registerTurn(ctx, chat.ID, "archive:"+chatmodel.NewID())
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(chat.ID, control)
	if err = archiver.ArchiveSession(ctx, agent.AgentRequest{
		ChatID: chat.ID, Backend: chat.Backend, WorkingDirectory: directory,
		NativeSessionID: chat.NativeSessionID, NativeSessionPath: chat.NativeSessionPath,
	}); err != nil {
		apiError(w, 502, "Could not archive chat")
		return
	}
	// Reload provider-owned metadata. No Spool archive flag or tombstone is
	// written; navigation is a projection of the native catalogue.
	if err = a.store.syncLocalSessionsContext(ctx, true); err != nil || !a.store.nativeSessionArchived(chat.Backend, chat.NativeSessionID) {
		apiError(w, 502, "Chat was archived, but the native session list could not be refreshed")
		return
	}
	navigation, err := a.store.UserNavigation(user)
	if err != nil {
		apiError(w, 502, "Chat was archived, but the native session list could not be refreshed")
		return
	}
	apiJSON(w, 200, navigation)
}

func (s *Store) nativeSessionArchived(backend chatmodel.Backend, id string) bool {
	s.nativeMu.RLock()
	defer s.nativeMu.RUnlock()
	return s.nativeSessions[string(backend)+":"+id].Archived
}
